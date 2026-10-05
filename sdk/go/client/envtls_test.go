package client

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/peer"
)

type pki struct {
	dir    string
	caCert *x509.Certificate
	caKey  *ecdsa.PrivateKey
	caFile string
}

func newPKI(t *testing.T) *pki {
	t.Helper()
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "test CA"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		KeyUsage: x509.KeyUsageCertSign, BasicConstraintsValid: true, IsCA: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, _ := x509.ParseCertificate(der)
	p := &pki{dir: t.TempDir(), caCert: cert, caKey: key}
	p.caFile = filepath.Join(p.dir, "ca.crt")
	writePEM(t, p.caFile, "CERTIFICATE", der)
	return p
}

func writePEM(t *testing.T, path, typ string, der []byte) {
	t.Helper()
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: typ, Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
}

// issue writes <name>.crt/<name>.key signed by the CA and returns the paths.
func (p *pki) issue(t *testing.T, name string) (string, string) {
	t.Helper()
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()), Subject: pkix.Name{CommonName: name},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		KeyUsage:    x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth},
		IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, DNSNames: []string{"localhost"},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, p.caCert, &key.PublicKey, p.caKey)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, _ := x509.MarshalPKCS8PrivateKey(key)
	certFile, keyFile := filepath.Join(p.dir, name+".crt"), filepath.Join(p.dir, name+".key")
	writePEM(t, certFile, "CERTIFICATE", der)
	writePEM(t, keyFile, "PRIVATE KEY", keyDER)
	return certFile, keyFile
}

// startMTLSServer requires a client certificate from the CA and reports the
// client's CN on peerCN.
func startMTLSServer(t *testing.T, p *pki) (string, chan string) {
	t.Helper()
	certFile, keyFile := p.issue(t, "muxcored")
	pair, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(p.caCert)
	peerCN := make(chan string, 4)
	srv := grpc.NewServer(
		grpc.Creds(credentials.NewTLS(&tls.Config{
			Certificates: []tls.Certificate{pair}, ClientAuth: tls.RequireAndVerifyClientCert,
			ClientCAs: pool, MinVersion: tls.VersionTLS12,
		})),
		grpc.UnaryInterceptor(func(ctx context.Context, req any, _ *grpc.UnaryServerInfo, h grpc.UnaryHandler) (any, error) {
			if pr, ok := peer.FromContext(ctx); ok {
				if ti, ok := pr.AuthInfo.(credentials.TLSInfo); ok && len(ti.State.VerifiedChains) > 0 {
					peerCN <- ti.State.VerifiedChains[0][0].Subject.CommonName
				}
			}
			return h(ctx, req)
		}),
	)
	healthpb.RegisterHealthServer(srv, health.NewServer())
	var lc net.ListenConfig
	lis, err := lc.Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)
	return lis.Addr().String(), peerCN
}

func clearTLSEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{EnvTLSCert, EnvTLSKey, EnvTLSCA, EnvTLSServerName, envInsecure, envInsecureOld, envProfile, "MUXCORE_MODULE_ID"} {
		t.Setenv(k, "")
	}
}

// ADR-0017: Dial picks up the identity exported by meshid.Ensure.
func TestDial_TLSFromEnv(t *testing.T) {
	clearTLSEnv(t)
	p := newPKI(t)
	addr, peerCN := startMTLSServer(t, p)
	certFile, keyFile := p.issue(t, "media-movies")
	t.Setenv(EnvTLSCert, certFile)
	t.Setenv(EnvTLSKey, keyFile)
	t.Setenv(EnvTLSCA, p.caFile)

	c, err := Dial(addr)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer c.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := healthpb.NewHealthClient(c.conn).Check(ctx, &healthpb.HealthCheckRequest{}); err != nil {
		t.Fatalf("RPC over env TLS: %v", err)
	}
	if cn := <-peerCN; cn != "media-movies" {
		t.Fatalf("server saw client CN %q", cn)
	}
}

func TestTransportCredentialsFromEnv(t *testing.T) {
	p := newPKI(t)
	certFile, keyFile := p.issue(t, "m")
	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

	if c, err := TransportCredentialsFromEnv(env(nil)); c != nil || err != nil {
		t.Fatalf("empty env: %v %v", c, err)
	}
	c, err := TransportCredentialsFromEnv(env(map[string]string{envInsecure: "true"}))
	if err != nil || c == nil || c.Info().SecurityProtocol != "insecure" {
		t.Fatalf("insecure flag: %v %v", c, err)
	}
	if _, err := TransportCredentialsFromEnv(env(map[string]string{envInsecure: "1", envProfile: "household"})); !errors.Is(err, ErrInsecureInHousehold) {
		t.Fatalf("household insecure: %v", err)
	}
	if _, err := TransportCredentialsFromEnv(env(map[string]string{EnvTLSCert: certFile})); err == nil {
		t.Fatal("cert without key accepted")
	}
	if _, err := TransportCredentialsFromEnv(env(map[string]string{EnvTLSCA: certFile + ".missing"})); err == nil {
		t.Fatal("missing CA accepted")
	}
	c, err = TransportCredentialsFromEnv(env(map[string]string{EnvTLSCert: certFile, EnvTLSKey: keyFile, EnvTLSCA: p.caFile}))
	if err != nil || c == nil || c.Info().SecurityProtocol != "tls" {
		t.Fatalf("tls: %v %v", c, err)
	}
}

// T-M3-02d: WithInsecure in the household profile is a clear error.
func TestDial_HouseholdInsecure(t *testing.T) {
	clearTLSEnv(t)
	t.Setenv(envProfile, "household")
	if _, err := Dial("localhost:19999", WithInsecure()); !errors.Is(err, ErrInsecureInHousehold) {
		t.Fatalf("want ErrInsecureInHousehold, got %v", err)
	}
	t.Setenv(envProfile, "dev")
	c, err := Dial("localhost:19999", WithInsecure())
	if err != nil {
		t.Fatalf("dev insecure: %v", err)
	}
	_ = c.Close()
}

// The plaintext env flag works without WithInsecure (dev convenience).
func TestDial_EnvInsecure(t *testing.T) {
	clearTLSEnv(t)
	t.Setenv(envInsecure, "true")
	c, err := Dial("localhost:19999")
	if err != nil {
		t.Fatalf("env insecure: %v", err)
	}
	_ = c.Close()
}
