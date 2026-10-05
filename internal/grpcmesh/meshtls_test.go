package grpcmesh

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/health/grpc_health_v1"
)

// startTLSHealthServer serves the standard health probe with creds and
// records the VerifiedModuleID of each caller.
func startTLSHealthServer(t *testing.T, creds credentials.TransportCredentials) (addr string, seen func() []string) {
	t.Helper()
	var mu sync.Mutex
	var ids []string
	srv := grpc.NewServer(grpc.Creds(creds), grpc.UnaryInterceptor(
		func(ctx context.Context, req any, _ *grpc.UnaryServerInfo, h grpc.UnaryHandler) (any, error) {
			id, _ := VerifiedModuleID(ctx)
			mu.Lock()
			ids = append(ids, id)
			mu.Unlock()
			return h(ctx, req)
		}))
	RegisterStandardHealthProbe(srv)
	var lc net.ListenConfig
	lis, err := lc.Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)
	return lis.Addr().String(), func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), ids...)
	}
}

func healthCheck(t *testing.T, addr string, creds credentials.TransportCredentials) error {
	t.Helper()
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(creds))
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err = grpc_health_v1.NewHealthClient(conn).Check(ctx, &grpc_health_v1.HealthCheckRequest{})
	return err
}

// sidecarServer starts a "sidecar" whose server certificate is cert.
func sidecarServer(t *testing.T, cert tls.Certificate) string {
	t.Helper()
	addr, _ := startTLSHealthServer(t, credentials.NewTLS(&tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS12,
	}))
	return addr
}

func TestSidecarClientTLS_PinsModuleCN(t *testing.T) {
	ca := testCA(t)
	sc, err := NewSidecarClientTLS(ca.CorePool(), "", "")
	if err != nil {
		t.Fatal(err)
	}
	addr := sidecarServer(t, moduleTLSCert(t, ca, "auth-local"))

	if err := healthCheck(t, addr, sc.Credentials("auth-local")); err != nil {
		t.Fatalf("matching CN should be accepted: %v", err)
	}
	err = healthCheck(t, addr, sc.Credentials("call-policy-default"))
	if err == nil || !strings.Contains(err.Error(), "identity mismatch") {
		t.Fatalf("wrong CN should be rejected with identity mismatch, got %v", err)
	}
}

func TestSidecarClientTLS_RejectsForeignCA(t *testing.T) {
	ca := testCA(t)
	sc, err := NewSidecarClientTLS(ca.CorePool(), "", "")
	if err != nil {
		t.Fatal(err)
	}
	// Right CN, but self-signed (not from the core CA).
	addr := sidecarServer(t, selfSignedTLSCert(t, "auth-local"))
	if err := healthCheck(t, addr, sc.Credentials("auth-local")); err == nil {
		t.Fatal("certificate from a foreign CA must be rejected")
	}
	// Another core CA (e.g. a different install) is foreign too.
	other := testCA(t)
	addr2 := sidecarServer(t, moduleTLSCert(t, other, "auth-local"))
	if err := healthCheck(t, addr2, sc.Credentials("auth-local")); err == nil {
		t.Fatal("certificate from another CA must be rejected")
	}
}

func TestVerifySidecarPeer_Errors(t *testing.T) {
	ca := testCA(t)
	if err := verifySidecarPeer(tls.ConnectionState{}, ca.CorePool(), "m"); err == nil {
		t.Fatal("expected error without peer certificates")
	}
	c := moduleTLSCert(t, ca, "m")
	leaf := c.Leaf
	if leaf == nil {
		t.Skip("tls.Certificate.Leaf not populated")
	}
	cs := tls.ConnectionState{PeerCertificates: []*x509.Certificate{leaf}}
	if err := verifySidecarPeer(cs, ca.CorePool(), ""); !errors.Is(err, ErrSidecarIdentity) {
		t.Fatalf("empty module ID must not match: %v", err)
	}
	if err := verifySidecarPeer(cs, ca.CorePool(), "m"); err != nil {
		t.Fatalf("valid peer: %v", err)
	}
}

func TestServerTLSCredentials_VerifyClientCertIfGiven(t *testing.T) {
	ca := testCA(t)
	dir := t.TempDir()
	certPath, keyPath, err := ca.IssueServerCertForDir("muxcored", dir,
		[]net.IP{net.ParseIP("127.0.0.1")}, []string{"localhost", "muxcore"})
	if err != nil {
		t.Fatal(err)
	}
	creds, err := ServerTLSCredentials(certPath, keyPath, ca.CorePool(), false)
	if err != nil {
		t.Fatal(err)
	}
	addr, seen := startTLSHealthServer(t, creds)

	// Client verifies core with the CA (server name localhost is a SAN).
	withCert := credentials.NewTLS(&tls.Config{
		RootCAs:      ca.CorePool(),
		ServerName:   "localhost",
		Certificates: []tls.Certificate{moduleTLSCert(t, ca, "media-movies")},
		MinVersion:   tls.VersionTLS12,
	})
	if err := healthCheck(t, addr, withCert); err != nil {
		t.Fatalf("client with core-CA cert: %v", err)
	}
	noCert := credentials.NewTLS(&tls.Config{RootCAs: ca.CorePool(), ServerName: "muxcore", MinVersion: tls.VersionTLS12})
	if err := healthCheck(t, addr, noCert); err != nil {
		t.Fatalf("client without cert must still connect (bootstrap/health): %v", err)
	}
	// Force the foreign certificate to be sent (Go clients otherwise skip
	// certificates that do not match the server's acceptable CAs).
	foreignCert := selfSignedTLSCert(t, "media-movies")
	foreign := credentials.NewTLS(&tls.Config{
		RootCAs:    ca.CorePool(),
		ServerName: "localhost",
		GetClientCertificate: func(*tls.CertificateRequestInfo) (*tls.Certificate, error) {
			return &foreignCert, nil
		},
		MinVersion: tls.VersionTLS12,
	})
	if err := healthCheck(t, addr, foreign); err == nil {
		t.Fatal("client presenting a foreign certificate must be rejected")
	}
	ids := seen()
	if len(ids) != 2 || ids[0] != "media-movies" || ids[1] != "" {
		t.Fatalf("VerifiedModuleID per call = %q, want [media-movies, \"\"]", ids)
	}
}

func TestServerTLSCredentials_RequireClientCert(t *testing.T) {
	ca := testCA(t)
	certPath, keyPath, err := ca.IssueServerCertForDir("muxcored", t.TempDir(),
		[]net.IP{net.ParseIP("127.0.0.1")}, []string{"localhost"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ServerTLSCredentials(certPath, keyPath, nil, true); err == nil {
		t.Fatal("mTLS without a client CA must fail")
	}
	if _, err := ServerTLSCredentials("", keyPath, nil, false); err == nil {
		t.Fatal("missing cert must fail")
	}
	creds, err := ServerTLSCredentials(certPath, keyPath, ca.CorePool(), true)
	if err != nil {
		t.Fatal(err)
	}
	addr, _ := startTLSHealthServer(t, creds)
	noCert := credentials.NewTLS(&tls.Config{RootCAs: ca.CorePool(), ServerName: "localhost", MinVersion: tls.VersionTLS12})
	if err := healthCheck(t, addr, noCert); err == nil {
		t.Fatal("mtls_enabled must require a client certificate")
	}
}

func TestExportCACertAndAppendCAFile(t *testing.T) {
	ca := testCA(t)
	dir := filepath.Join(t.TempDir(), "export")
	path, err := ca.ExportCACert(dir)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != string(ca.CACertPEM()) {
		t.Fatalf("exported CA mismatch: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "ca.key")); !os.IsNotExist(err) {
		t.Fatal("CA key must never be exported")
	}
	if _, err := ca.ExportCACert(""); err == nil {
		t.Fatal("empty export dir must fail")
	}
	pool := ca.CorePool()
	if err := AppendCAFile(pool, path); err != nil {
		t.Fatal(err)
	}
	if err := AppendCAFile(pool, ""); err != nil {
		t.Fatal(err)
	}
	bad := filepath.Join(t.TempDir(), "bad.pem")
	_ = os.WriteFile(bad, []byte("nope"), 0o600)
	if err := AppendCAFile(pool, bad); err == nil {
		t.Fatal("non-PEM CA file must fail")
	}
	if _, err := NewSidecarClientTLS(pool, bad, bad); err == nil {
		t.Fatal("bad client cert must fail")
	}
}
