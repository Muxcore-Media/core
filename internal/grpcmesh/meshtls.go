package grpcmesh

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"

	"google.golang.org/grpc/credentials"
)

// strongCipherSuites restricts TLS 1.2 handshakes to AEAD suites (TLS 1.3
// suites are always AEAD).
var strongCipherSuites = []uint16{
	tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,
	tls.TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384,
	tls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256,
	tls.TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384,
}

// IssueServerCertForDir issues a certificate for id with the given SANs and
// writes it to dir/module.crt and dir/module.key (0600). Used for core's own
// server certificate, which needs the hostnames modules dial.
func (ca *CertAuthority) IssueServerCertForDir(id, dir string, ips []net.IP, dnsNames []string) (string, string, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", "", fmt.Errorf("create cert dir for %s: %w", id, err)
	}
	certPEM, keyPEM, err := ca.IssueModuleCert(id, ips, dnsNames)
	if err != nil {
		return "", "", err
	}
	certPath := filepath.Join(dir, "module.crt")
	keyPath := filepath.Join(dir, "module.key")
	if err := os.WriteFile(certPath, certPEM, 0o600); err != nil {
		return "", "", err
	}
	if err := os.WriteFile(keyPath, keyPEM, 0o600); err != nil {
		return "", "", err
	}
	return certPath, keyPath, nil
}

// ExportCACert writes the CA's public certificate to dir/ca.crt (0644) so
// modules and operators can verify core (ADR-0016 MUXCORE_CA_EXPORT_DIR).
// The private key is never exported.
func (ca *CertAuthority) ExportCACert(dir string) (string, error) {
	if dir == "" {
		return "", errors.New("export dir is empty")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil { //nolint:gosec // export dir holds only the public CA certificate
		return "", fmt.Errorf("create CA export dir: %w", err)
	}
	path := filepath.Join(dir, "ca.crt")
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, ca.CACertPEM(), 0o644); err != nil { //nolint:gosec // public CA certificate, meant to be world-readable
		return "", fmt.Errorf("write exported CA cert: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return "", fmt.Errorf("install exported CA cert: %w", err)
	}
	return path, nil
}

// CorePool returns a cert pool containing the CA certificate.
func (ca *CertAuthority) CorePool() *x509.CertPool {
	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM(ca.CACertPEM())
	return pool
}

// AppendCAFile adds the PEM certificates in path to pool. An empty path is a
// no-op.
func AppendCAFile(pool *x509.CertPool, path string) error {
	if path == "" {
		return nil
	}
	pemData, err := os.ReadFile(path) //nolint:gosec // path from operator TLS configuration
	if err != nil {
		return fmt.Errorf("read CA cert %q: %w", path, err)
	}
	if !pool.AppendCertsFromPEM(pemData) {
		return fmt.Errorf("no certificates found in %q", path)
	}
	return nil
}

// ServerTLSCredentials builds gRPC server credentials presenting the given
// certificate. When clientCAs is non-nil, client certificates are verified
// against it: required when requireClientCert is set (mtls_enabled), otherwise
// verified only if presented (tls.VerifyClientCertIfGiven, ADR-0016) so that
// VerifiedModuleID can identify modules while BootstrapRegister and health
// checks still work without a certificate.
func ServerTLSCredentials(certFile, keyFile string, clientCAs *x509.CertPool, requireClientCert bool) (credentials.TransportCredentials, error) {
	if certFile == "" || keyFile == "" {
		return nil, errors.New("server TLS needs both a certificate and a key file")
	}
	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return nil, fmt.Errorf("load TLS cert/key: %w", err)
	}
	cfg := &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS12,
		CipherSuites: strongCipherSuites,
	}
	switch {
	case requireClientCert && clientCAs == nil:
		return nil, errors.New("mTLS is enabled but no client CA is available")
	case requireClientCert:
		cfg.ClientAuth = tls.RequireAndVerifyClientCert
		cfg.ClientCAs = clientCAs
	case clientCAs != nil:
		cfg.ClientAuth = tls.VerifyClientCertIfGiven
		cfg.ClientCAs = clientCAs
	}
	return credentials.NewTLS(cfg), nil
}

// SidecarClientTLS builds client credentials for core's outbound dials to
// sidecar providers (policy, auth, storage). Core verifies the sidecar's
// certificate chain against the core CA (plus any configured CA) and pins the
// certificate's Common Name to the module ID the sidecar registered with, and
// presents its own certificate so the sidecar can verify core. Immutable
// after construction and safe for concurrent use.
type SidecarClientTLS struct {
	roots *x509.CertPool
	certs []tls.Certificate
}

// NewSidecarClientTLS returns sidecar client credentials. roots is the pool
// sidecar certificates must chain to (nil = system roots); certFile/keyFile
// is core's client certificate (both empty = present none).
func NewSidecarClientTLS(roots *x509.CertPool, certFile, keyFile string) (*SidecarClientTLS, error) {
	s := &SidecarClientTLS{roots: roots}
	if certFile != "" || keyFile != "" {
		cert, err := tls.LoadX509KeyPair(certFile, keyFile)
		if err != nil {
			return nil, fmt.Errorf("load core client cert: %w", err)
		}
		s.certs = []tls.Certificate{cert}
	}
	return s, nil
}

// ErrSidecarIdentity is returned (wrapped) when a sidecar's certificate does
// not belong to the module core dialled.
var ErrSidecarIdentity = errors.New("sidecar certificate identity mismatch")

// TLSConfig returns the client tls.Config for dialling moduleID.
//
// Hostname verification is replaced by identity verification: sidecars are
// dialled at whatever address they registered (container names, loopback,
// LAN IPs) and core-issued certificates carry the module ID as CN, so the
// chain is verified against the configured roots and the leaf CN must equal
// moduleID (ADR-0017 decision 1).
func (s *SidecarClientTLS) TLSConfig(moduleID string) *tls.Config {
	roots := s.roots
	return &tls.Config{
		MinVersion:   tls.VersionTLS12,
		CipherSuites: strongCipherSuites,
		Certificates: s.certs,
		// Chain and identity are verified in VerifyConnection below; the
		// standard check would verify the dial hostname instead of the
		// module identity.
		InsecureSkipVerify: true, //nolint:gosec // G402: full chain + CN verification in VerifyConnection
		VerifyConnection: func(cs tls.ConnectionState) error {
			return verifySidecarPeer(cs, roots, moduleID)
		},
	}
}

// Credentials returns gRPC transport credentials for dialling moduleID.
func (s *SidecarClientTLS) Credentials(moduleID string) credentials.TransportCredentials {
	return credentials.NewTLS(s.TLSConfig(moduleID))
}

func verifySidecarPeer(cs tls.ConnectionState, roots *x509.CertPool, moduleID string) error {
	if len(cs.PeerCertificates) == 0 {
		return errors.New("sidecar presented no certificate")
	}
	leaf := cs.PeerCertificates[0]
	inter := x509.NewCertPool()
	for _, c := range cs.PeerCertificates[1:] {
		inter.AddCert(c)
	}
	if _, err := leaf.Verify(x509.VerifyOptions{
		Roots:         roots,
		Intermediates: inter,
		KeyUsages:     []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}); err != nil {
		return fmt.Errorf("verify sidecar certificate for %q: %w", moduleID, err)
	}
	cn := strings.TrimSpace(leaf.Subject.CommonName)
	if moduleID == "" || cn != moduleID {
		return fmt.Errorf("%w: dialled module %q, certificate CN %q", ErrSidecarIdentity, moduleID, cn)
	}
	return nil
}
