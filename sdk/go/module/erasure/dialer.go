package erasure

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/metadata"

	"github.com/Muxcore-Media/core/pkg/contracts"
	authv1 "github.com/Muxcore-Media/core/proto/gen/muxcore/auth/v1"
	discoveryv1 "github.com/Muxcore-Media/core/proto/gen/muxcore/discovery/v1"
	"github.com/Muxcore-Media/core/sdk/go/module/meshtls"
)

// CapabilityFinder is the part of core's DiscoveryService the dialer uses.
// discoveryv1.NewDiscoveryServiceClient(coreConn) satisfies it, as does
// sdk/go/client's Client.Discovery.Raw().
type CapabilityFinder interface {
	FindByCapability(ctx context.Context, in *discoveryv1.FindByCapabilityRequest, opts ...grpc.CallOption) (*discoveryv1.FindByCapabilityResponse, error)
}

// Errors returned by the dialer.
var (
	// ErrNoProvider: core reports no module with the identity capability.
	ErrNoProvider = errors.New("erasure: no identity provider registered with core")
	// ErrAmbiguousProvider: core reports more than one identity provider.
	// The capability is exclusive; the dialer never picks one.
	ErrAmbiguousProvider = errors.New("erasure: more than one identity provider registered with core")
	// ErrProviderIdentity: the server's verified certificate is not the
	// discovered provider's.
	ErrProviderIdentity = errors.New("erasure: identity provider certificate mismatch")
	// ErrTLSConfig: the module's mesh TLS material is missing or unusable.
	ErrTLSConfig = errors.New("erasure: mesh TLS configuration")
)

// Provider is the discovered identity provider.
type Provider struct {
	// ModuleID is the provider's module id: the only identity its
	// certificate CN may carry.
	ModuleID string
	// Addr is the gRPC address dialled.
	Addr string
}

// ProviderDialer discovers the identity provider through core and dials it
// with the identity checks described in the package documentation.
type ProviderDialer struct {
	// Discovery is core's DiscoveryService client. Required.
	Discovery CapabilityFinder
	// Getenv defaults to os.Getenv.
	Getenv func(string) string
	// CertFile, KeyFile and CAFile override MUXCORE_TLS_CERT, MUXCORE_TLS_KEY
	// and MUXCORE_TLS_CA.
	CertFile string
	KeyFile  string
	CAFile   string
}

// Conn is a connection to the verified identity provider.
type Conn struct {
	Client   authv1.AuthServiceClient
	conn     *grpc.ClientConn
	Provider Provider
}

// Close releases the connection.
func (c *Conn) Close() error { return c.conn.Close() }

func (d *ProviderDialer) getenv(k string) string {
	if d.Getenv != nil {
		return d.Getenv(k)
	}
	return os.Getenv(k)
}

// Discover asks core for the provider of the exclusive "identity" capability.
func (d *ProviderDialer) Discover(ctx context.Context) (Provider, error) {
	if d == nil || d.Discovery == nil {
		return Provider{}, errors.New("erasure: ProviderDialer.Discovery is required")
	}
	resp, err := d.Discovery.FindByCapability(ctx, &discoveryv1.FindByCapabilityRequest{Capability: contracts.CapabilityIdentity})
	if err != nil {
		return Provider{}, fmt.Errorf("erasure: discover identity provider: %w", err)
	}
	var found Provider
	for _, m := range resp.GetModules() {
		id := strings.TrimSpace(m.GetId())
		if id == "" {
			continue
		}
		if found.ModuleID != "" && found.ModuleID != id {
			return Provider{}, fmt.Errorf("%w: %q and %q", ErrAmbiguousProvider, found.ModuleID, id)
		}
		if found.Addr != "" {
			continue
		}
		found.ModuleID = id
		found.Addr = dialAddr(id, strings.TrimSpace(m.GetHttpAddr()), d.getenv("MUXCORE_MESH_DIAL_LOCAL") == "true")
	}
	if found.ModuleID == "" {
		return Provider{}, ErrNoProvider
	}
	if found.Addr == "" {
		return Provider{}, fmt.Errorf("%w: %q advertises no address", ErrNoProvider, found.ModuleID)
	}
	return found, nil
}

// dialAddr turns an advertised listen address into a dial address: a
// wildcard host becomes the module id (container DNS) or, with
// MUXCORE_MESH_DIAL_LOCAL=true, loopback. The address never changes the
// identity the provider must prove.
func dialAddr(moduleID, advertised string, local bool) string {
	if advertised == "" {
		return ""
	}
	host, port, err := net.SplitHostPort(advertised)
	if err != nil || port == "" {
		return advertised
	}
	if host != "" && host != "0.0.0.0" && host != "::" {
		return advertised
	}
	if local {
		return net.JoinHostPort("127.0.0.1", port)
	}
	return net.JoinHostPort(moduleID, port)
}

// Dial discovers the provider and connects to it as callerID (the owner's
// module id). The connection is lazy: identity failures surface on the
// first RPC, which then fails without returning any ledger data.
func (d *ProviderDialer) Dial(ctx context.Context, callerID string) (*Conn, error) {
	p, err := d.Discover(ctx)
	if err != nil {
		return nil, err
	}
	creds, err := d.transport(p.ModuleID, callerID)
	if err != nil {
		return nil, err
	}
	conn, err := grpc.NewClient(p.Addr, creds, grpc.WithUnaryInterceptor(callerIDInterceptor(callerID)))
	if err != nil {
		return nil, fmt.Errorf("erasure: dial identity provider %s at %s: %w", p.ModuleID, p.Addr, err)
	}
	return &Conn{Client: authv1.NewAuthServiceClient(conn), conn: conn, Provider: p}, nil
}

// callerIDInterceptor adds x-caller-id, which only the dev profile trusts
// (ADR-0017 §2); with mTLS the provider uses the certificate CN.
func callerIDInterceptor(callerID string) grpc.UnaryClientInterceptor {
	return func(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
		return invoker(metadata.AppendToOutgoingContext(ctx, "x-caller-id", callerID), method, req, reply, cc, opts...)
	}
}

func (d *ProviderDialer) transport(providerID, callerID string) (grpc.DialOption, error) {
	if meshtls.Insecure() {
		// Exactly meshtls's rule: plaintext with the explicit dev flag,
		// refused in the household and staging profiles.
		return meshtls.DialOption(providerID)
	}
	cfg, err := d.TLSConfig(providerID, callerID)
	if err != nil {
		return nil, err
	}
	return grpc.WithTransportCredentials(credentials.NewTLS(cfg)), nil
}

func (d *ProviderDialer) pick(v, env string) string {
	if v != "" {
		return v
	}
	return strings.TrimSpace(d.getenv(env))
}

// TLSConfig returns the client TLS configuration for dialling providerID as
// callerID: chain verified against the mesh CA only, certificate valid for
// providerID, verified leaf CN equal to providerID, and the caller's own
// certificate (CN callerID) presented.
func (d *ProviderDialer) TLSConfig(providerID, callerID string) (*tls.Config, error) {
	if providerID == "" || callerID == "" {
		return nil, fmt.Errorf("%w: provider and caller module ids are required", ErrTLSConfig)
	}
	caFile := d.pick(d.CAFile, meshtls.EnvTLSCA)
	certFile := d.pick(d.CertFile, meshtls.EnvTLSCert)
	keyFile := d.pick(d.KeyFile, meshtls.EnvTLSKey)
	if caFile == "" {
		return nil, fmt.Errorf("%w: %s is required; system roots are never trusted for the erasure ledger", ErrTLSConfig, meshtls.EnvTLSCA)
	}
	if certFile == "" || keyFile == "" {
		return nil, fmt.Errorf("%w: %s and %s are required; the provider identifies the caller by its certificate", ErrTLSConfig, meshtls.EnvTLSCert, meshtls.EnvTLSKey)
	}
	caPEM, err := os.ReadFile(caFile) //nolint:gosec // operator-configured CA path
	if err != nil {
		return nil, fmt.Errorf("%w: read CA: %w", ErrTLSConfig, err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(caPEM) {
		return nil, fmt.Errorf("%w: no certificates in %s", ErrTLSConfig, caFile)
	}
	pair, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return nil, fmt.Errorf("%w: load client certificate: %w", ErrTLSConfig, err)
	}
	if pair.Leaf == nil || pair.Leaf.Subject.CommonName != callerID {
		cn := ""
		if pair.Leaf != nil {
			cn = pair.Leaf.Subject.CommonName
		}
		return nil, fmt.Errorf("%w: client certificate CN %q is not this module (%q)", ErrTLSConfig, cn, callerID)
	}
	return &tls.Config{
		MinVersion:   tls.VersionTLS12,
		RootCAs:      roots,
		Certificates: []tls.Certificate{pair},
		// Standard verification (chain to the mesh CA, validity, server-auth
		// EKU, providerID as a SAN) runs first; VerifyConnection then pins
		// the identity to the CN.
		ServerName: providerID,
		VerifyConnection: func(cs tls.ConnectionState) error {
			return verifyProviderCN(cs, providerID)
		},
	}, nil
}

func verifyProviderCN(cs tls.ConnectionState, providerID string) error {
	if len(cs.VerifiedChains) == 0 || len(cs.VerifiedChains[0]) == 0 {
		return fmt.Errorf("%w: no verified chain", ErrProviderIdentity)
	}
	if cn := cs.VerifiedChains[0][0].Subject.CommonName; cn != providerID {
		return fmt.Errorf("%w: expected %q, certificate CN %q", ErrProviderIdentity, providerID, cn)
	}
	return nil
}
