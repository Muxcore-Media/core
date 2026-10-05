// Package meshtls gives module sidecars one place to build gRPC transport
// security from the MUXCORE_* environment, so a module serves and dials TLS
// with the identity it enrolled with (ADR-0017) instead of plaintext.
//
// Environment (shared with sdk/go/client):
//
//	MUXCORE_INSECURE_DISABLE_TLS, MUXCORE_DEV_TLS_SKIP, MUXCORE_GRPC_INSECURE
//	               "true"/"1" selects plaintext (dev profile only)
//	MUXCORE_TLS_CERT, MUXCORE_TLS_KEY  this module's certificate and key
//	MUXCORE_TLS_CA                     CA bundle: verifies peers (dial) and
//	                                   client certificates (serve)
//	MUXCORE_TLS_SERVER_NAME            overrides the name verified when dialling
//
// Servers request but do not require a client certificate
// (tls.VerifyClientCertIfGiven); callers are authorised by core, not by the
// transport alone (ADR-0016). Sidecars register under service names that are
// SANs of their enrolled certificate (MUXCORE_ENROLL_DNS_NAMES=<service>), so
// Dial verifies the host part of the target.
package meshtls

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
)

// Environment variable names.
const (
	EnvTLSCert       = "MUXCORE_TLS_CERT"
	EnvTLSKey        = "MUXCORE_TLS_KEY"
	EnvTLSCA         = "MUXCORE_TLS_CA"
	EnvTLSServerName = "MUXCORE_TLS_SERVER_NAME"
)

// ErrInsecureInHousehold is returned when plaintext is requested while
// MUXCORE_PROFILE is household or staging.
var ErrInsecureInHousehold = errors.New("meshtls: plaintext gRPC is not allowed in the household profile — " +
	"unset MUXCORE_INSECURE_DISABLE_TLS and use the module's mesh identity, or set MUXCORE_PROFILE=dev")

func truthy(k string) bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv(k)))
	return v == "true" || v == "1"
}

// Insecure reports whether plaintext gRPC is explicitly enabled.
func Insecure() bool {
	return truthy("MUXCORE_INSECURE_DISABLE_TLS") || truthy("MUXCORE_DEV_TLS_SKIP") || truthy("MUXCORE_GRPC_INSECURE")
}

func checkProfile() error {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("MUXCORE_PROFILE"))) {
	case "household", "staging":
		return ErrInsecureInHousehold
	}
	return nil
}

func loadKeyPair(required bool) (*tls.Certificate, error) {
	certFile := strings.TrimSpace(os.Getenv(EnvTLSCert))
	keyFile := strings.TrimSpace(os.Getenv(EnvTLSKey))
	if certFile == "" && keyFile == "" && !required {
		return nil, nil
	}
	if certFile == "" || keyFile == "" {
		return nil, fmt.Errorf("meshtls: %s and %s must both be set", EnvTLSCert, EnvTLSKey)
	}
	pair, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return nil, fmt.Errorf("meshtls: load %s/%s: %w", EnvTLSCert, EnvTLSKey, err)
	}
	return &pair, nil
}

func loadCA() (*x509.CertPool, error) {
	caFile := strings.TrimSpace(os.Getenv(EnvTLSCA))
	if caFile == "" {
		return nil, nil
	}
	data, err := os.ReadFile(caFile) //nolint:gosec // operator-configured CA path
	if err != nil {
		return nil, fmt.Errorf("meshtls: read %s: %w", EnvTLSCA, err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(data) {
		return nil, fmt.Errorf("meshtls: no certificates in %s (%s)", EnvTLSCA, caFile)
	}
	return pool, nil
}

// ServerOption returns the grpc.ServerOption for the module's listener: a
// no-op when plaintext is enabled, otherwise TLS from MUXCORE_TLS_CERT/KEY
// (required) with client certificates verified against MUXCORE_TLS_CA when
// the client presents one.
func ServerOption() (grpc.ServerOption, error) {
	if Insecure() {
		if err := checkProfile(); err != nil {
			return nil, err
		}
		return grpc.EmptyServerOption{}, nil
	}
	pair, err := loadKeyPair(true)
	if err != nil {
		return nil, err
	}
	cfg := &tls.Config{
		Certificates: []tls.Certificate{*pair},
		MinVersion:   tls.VersionTLS12,
	}
	pool, err := loadCA()
	if err != nil {
		return nil, err
	}
	if pool != nil {
		cfg.ClientCAs = pool
		cfg.ClientAuth = tls.VerifyClientCertIfGiven
	}
	return grpc.Creds(credentials.NewTLS(cfg)), nil
}

// DialOption returns the transport credentials for dialling a peer whose
// certificate carries serverName as a SAN. MUXCORE_TLS_SERVER_NAME overrides
// serverName. Without MUXCORE_TLS_CA the system roots are used; the client
// certificate is presented when MUXCORE_TLS_CERT/KEY are set.
func DialOption(serverName string) (grpc.DialOption, error) {
	if Insecure() {
		if err := checkProfile(); err != nil {
			return nil, err
		}
		return grpc.WithTransportCredentials(insecure.NewCredentials()), nil
	}
	cfg := &tls.Config{MinVersion: tls.VersionTLS12, ServerName: serverName}
	if v := strings.TrimSpace(os.Getenv(EnvTLSServerName)); v != "" {
		cfg.ServerName = v
	}
	pool, err := loadCA()
	if err != nil {
		return nil, err
	}
	cfg.RootCAs = pool // nil = system roots
	pair, err := loadKeyPair(false)
	if err != nil {
		return nil, err
	}
	if pair != nil {
		cfg.Certificates = []tls.Certificate{*pair}
	}
	return grpc.WithTransportCredentials(credentials.NewTLS(cfg)), nil
}

// NewServer creates a gRPC server with ServerOption followed by opts.
func NewServer(opts ...grpc.ServerOption) (*grpc.Server, error) {
	so, err := ServerOption()
	if err != nil {
		return nil, err
	}
	return grpc.NewServer(append([]grpc.ServerOption{so}, opts...)...), nil
}

// Dial creates a client connection to target (host:port, or a gRPC target
// with a scheme) verifying the peer as the host part of target.
func Dial(target string, opts ...grpc.DialOption) (*grpc.ClientConn, error) {
	do, err := DialOption(hostOf(target))
	if err != nil {
		return nil, err
	}
	return grpc.NewClient(target, append([]grpc.DialOption{do}, opts...)...)
}

func hostOf(target string) string {
	t := target
	if i := strings.Index(t, "://"); i >= 0 {
		t = t[i+3:]
		if j := strings.Index(t, "/"); j >= 0 { // skip the authority, keep the endpoint
			t = t[j+1:]
		}
	}
	if h, _, err := net.SplitHostPort(t); err == nil {
		return h
	}
	return strings.Trim(t, "[]")
}
