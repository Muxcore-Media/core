package meshtls

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
)

type pki struct {
	dir     string
	ca      *x509.Certificate
	caKey   *ecdsa.PrivateKey
	caFile  string
	counter int64
}

func newPKI(t *testing.T) *pki {
	t.Helper()
	k, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "test ca"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &k.PublicKey, k)
	if err != nil {
		t.Fatal(err)
	}
	ca, _ := x509.ParseCertificate(der)
	p := &pki{dir: t.TempDir(), ca: ca, caKey: k}
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

// issue writes a leaf cert/key valid for name and returns their paths.
func (p *pki) issue(t *testing.T, name string) (certFile, keyFile string) {
	t.Helper()
	p.counter++
	k, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(100 + p.counter), Subject: pkix.Name{CommonName: name},
		DNSNames:  []string{name},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		KeyUsage:    x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
	}
	if ip := net.ParseIP(name); ip != nil {
		tmpl.DNSNames, tmpl.IPAddresses = nil, []net.IP{ip}
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, p.ca, &k.PublicKey, p.caKey)
	if err != nil {
		t.Fatal(err)
	}
	kder, _ := x509.MarshalECPrivateKey(k)
	certFile = filepath.Join(p.dir, name+".crt")
	keyFile = filepath.Join(p.dir, name+".key")
	writePEM(t, certFile, "CERTIFICATE", der)
	writePEM(t, keyFile, "EC PRIVATE KEY", kder)
	return
}

func setEnv(t *testing.T, kv map[string]string) {
	t.Helper()
	for _, k := range []string{"MUXCORE_INSECURE_DISABLE_TLS", "MUXCORE_DEV_TLS_SKIP", "MUXCORE_GRPC_INSECURE",
		EnvTLSCert, EnvTLSKey, EnvTLSCA, EnvTLSServerName, "MUXCORE_PROFILE"} {
		t.Setenv(k, "")
	}
	for k, v := range kv {
		t.Setenv(k, v)
	}
}

func serve(t *testing.T) string {
	t.Helper()
	srv, err := NewServer()
	if err != nil {
		t.Fatal(err)
	}
	healthpb.RegisterHealthServer(srv, health.NewServer())
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)
	return lis.Addr().String()
}

func check(addr string, opts ...grpc.DialOption) error {
	conn, err := Dial(addr, opts...)
	if err != nil {
		return err
	}
	defer conn.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, err = healthpb.NewHealthClient(conn).Check(ctx, &healthpb.HealthCheckRequest{})
	return err
}

func TestMutualTLSRoundTrip(t *testing.T) {
	p := newPKI(t)
	sc, sk := p.issue(t, "127.0.0.1")
	cc, ck := p.issue(t, "client")
	setEnv(t, map[string]string{EnvTLSCert: sc, EnvTLSKey: sk, EnvTLSCA: p.caFile})
	addr := serve(t)

	setEnv(t, map[string]string{EnvTLSCert: cc, EnvTLSKey: ck, EnvTLSCA: p.caFile})
	if err := check(addr); err != nil {
		t.Fatalf("with client cert: %v", err)
	}
}

func TestClientWithoutCert(t *testing.T) {
	p := newPKI(t)
	sc, sk := p.issue(t, "127.0.0.1")
	setEnv(t, map[string]string{EnvTLSCert: sc, EnvTLSKey: sk, EnvTLSCA: p.caFile})
	addr := serve(t)

	setEnv(t, map[string]string{EnvTLSCA: p.caFile})
	if err := check(addr); err != nil {
		t.Fatalf("without client cert: %v", err)
	}
}

func TestServerNameOverride(t *testing.T) {
	p := newPKI(t)
	sc, sk := p.issue(t, "svc")
	setEnv(t, map[string]string{EnvTLSCert: sc, EnvTLSKey: sk, EnvTLSCA: p.caFile})
	addr := serve(t)

	setEnv(t, map[string]string{EnvTLSCA: p.caFile})
	if err := check(addr); err == nil {
		t.Fatal("expected name mismatch (cert is for svc only)")
	}
	setEnv(t, map[string]string{EnvTLSCA: p.caFile, EnvTLSServerName: "svc"})
	if err := check(addr); err != nil {
		t.Fatalf("override: %v", err)
	}
}

func TestWrongCAFails(t *testing.T) {
	p, other := newPKI(t), newPKI(t)
	sc, sk := p.issue(t, "127.0.0.1")
	setEnv(t, map[string]string{EnvTLSCert: sc, EnvTLSKey: sk, EnvTLSCA: p.caFile})
	addr := serve(t)

	setEnv(t, map[string]string{EnvTLSCA: other.caFile})
	if err := check(addr); err == nil {
		t.Fatal("expected verification failure with wrong CA")
	}
}

func TestInsecureRoundTrip(t *testing.T) {
	for _, k := range []string{"MUXCORE_INSECURE_DISABLE_TLS", "MUXCORE_DEV_TLS_SKIP", "MUXCORE_GRPC_INSECURE"} {
		setEnv(t, map[string]string{k: "true"})
		if !Insecure() {
			t.Fatalf("%s not honoured", k)
		}
		if err := check(serve(t)); err != nil {
			t.Fatalf("%s: %v", k, err)
		}
	}
}

func TestInsecureRefusedInHousehold(t *testing.T) {
	setEnv(t, map[string]string{"MUXCORE_INSECURE_DISABLE_TLS": "true", "MUXCORE_PROFILE": "household"})
	if _, err := ServerOption(); err != ErrInsecureInHousehold {
		t.Fatalf("server: %v", err)
	}
	if _, err := DialOption("x"); err != ErrInsecureInHousehold {
		t.Fatalf("dial: %v", err)
	}
}

func TestMissingCertKey(t *testing.T) {
	p := newPKI(t)
	sc, sk := p.issue(t, "s")
	for name, env := range map[string]map[string]string{
		"none":      {},
		"cert only": {EnvTLSCert: sc},
		"key only":  {EnvTLSKey: sk},
		"bad paths": {EnvTLSCert: "/nonexistent", EnvTLSKey: "/nonexistent"},
	} {
		setEnv(t, env)
		if _, err := ServerOption(); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
	setEnv(t, map[string]string{EnvTLSCert: sc}) // dial: half a pair is an error too
	if _, err := DialOption("s"); err == nil {
		t.Error("dial with cert only: expected error")
	}
	setEnv(t, map[string]string{EnvTLSCA: filepath.Join(p.dir, "missing")})
	if _, err := DialOption("s"); err == nil {
		t.Error("dial with missing CA: expected error")
	}
}

func TestHostOf(t *testing.T) {
	for in, want := range map[string]string{
		"core:9090": "core", "127.0.0.1:1": "127.0.0.1", "[::1]:9": "::1",
		"dns:///core:9090": "core", "passthrough:///a.b:1": "a.b", "name": "name",
	} {
		if got := hostOf(in); got != want {
			t.Errorf("hostOf(%q)=%q want %q", in, got, want)
		}
	}
}
