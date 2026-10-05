package meshid

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
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/peer"

	modulev1 "github.com/Muxcore-Media/core/proto/gen/muxcore/module/v1"
)

// testCA is a minimal core CA.
type testCA struct {
	cert   *x509.Certificate
	key    *ecdsa.PrivateKey
	pemCA  []byte
	caFile string
}

func newTestCA(t *testing.T) *testCA {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "MuxCore Internal CA"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, _ := x509.ParseCertificate(der)
	ca := &testCA{cert: cert, key: key, pemCA: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})}
	ca.caFile = filepath.Join(t.TempDir(), "ca.crt")
	if err := os.WriteFile(ca.caFile, ca.pemCA, 0o600); err != nil {
		t.Fatal(err)
	}
	return ca
}

func (ca *testCA) sign(t *testing.T, cn string, pub any, ips []net.IP, dns []string) []byte {
	t.Helper()
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: cn},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth},
		IPAddresses:  ips,
		DNSNames:     dns,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca.cert, pub, ca.key)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}

// fakeCore implements BootstrapRegister like core: single-use token, signs
// the CSR with CN = module ID. Register echoes the verified peer CN.
type fakeCore struct {
	modulev1.UnimplementedModuleRegistrationServer
	t         *testing.T
	ca        *testCA
	token     string
	used      atomic.Bool
	calls     atomic.Int32
	mu        sync.Mutex
	lastNames []string
	sawCert   atomic.Bool
}

func (f *fakeCore) BootstrapRegister(ctx context.Context, req *modulev1.BootstrapRegisterRequest) (*modulev1.BootstrapRegisterResponse, error) {
	f.calls.Add(1)
	if p, ok := peer.FromContext(ctx); ok {
		if ti, ok := p.AuthInfo.(credentials.TLSInfo); ok && len(ti.State.PeerCertificates) > 0 {
			f.sawCert.Store(true)
		}
	}
	if req.GetToken() != f.token || !f.used.CompareAndSwap(false, true) {
		return &modulev1.BootstrapRegisterResponse{Error: "invalid token: already used or unknown"}, nil
	}
	block, _ := pem.Decode([]byte(req.GetCsrPem()))
	if block == nil {
		return &modulev1.BootstrapRegisterResponse{Error: "no CSR"}, nil
	}
	csr, err := x509.ParseCertificateRequest(block.Bytes)
	if err != nil || csr.CheckSignature() != nil {
		return &modulev1.BootstrapRegisterResponse{Error: "bad CSR"}, nil
	}
	f.mu.Lock()
	f.lastNames = req.GetDnsNames()
	f.mu.Unlock()
	cert := f.ca.sign(f.t, req.GetModuleId(), csr.PublicKey, []net.IP{net.ParseIP("127.0.0.1")}, []string{"localhost", req.GetModuleId()})
	return &modulev1.BootstrapRegisterResponse{Accepted: true, SignedCert: string(cert), CaCert: string(f.ca.pemCA)}, nil
}

func (f *fakeCore) Register(ctx context.Context, req *modulev1.RegisterRequest) (*modulev1.RegisterResponse, error) {
	p, _ := peer.FromContext(ctx)
	ti, ok := p.AuthInfo.(credentials.TLSInfo)
	if !ok || len(ti.State.VerifiedChains) == 0 {
		return &modulev1.RegisterResponse{Error: "no verified client certificate"}, nil
	}
	return &modulev1.RegisterResponse{Accepted: true, NodeId: ti.State.VerifiedChains[0][0].Subject.CommonName}, nil
}

func startFakeCore(t *testing.T, ca *testCA, token string) (string, *fakeCore) {
	t.Helper()
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	certPEM := ca.sign(t, "muxcored", &key.PublicKey, []net.IP{net.ParseIP("127.0.0.1")}, []string{"localhost", "muxcored"})
	keyDER, _ := x509.MarshalPKCS8PrivateKey(key)
	pair, err := tls.X509KeyPair(certPEM, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}))
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM(ca.pemCA)
	srv := grpc.NewServer(grpc.Creds(credentials.NewTLS(&tls.Config{
		Certificates: []tls.Certificate{pair},
		ClientAuth:   tls.VerifyClientCertIfGiven,
		ClientCAs:    pool,
		MinVersion:   tls.VersionTLS12,
	})))
	fc := &fakeCore{t: t, ca: ca, token: token}
	modulev1.RegisterModuleRegistrationServer(srv, fc)
	var lc net.ListenConfig
	lis, err := lc.Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)
	return lis.Addr().String(), fc
}

// env is a fake process environment.
type env struct {
	mu sync.Mutex
	m  map[string]string
}

func newEnv(kv map[string]string) *env {
	e := &env{m: map[string]string{}}
	for k, v := range kv {
		e.m[k] = v
	}
	return e
}

func (e *env) get(k string) string { e.mu.Lock(); defer e.mu.Unlock(); return e.m[k] }
func (e *env) set(k, v string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.m[k] = v
	return nil
}

func perm(t *testing.T, path string) os.FileMode {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return info.Mode().Perm()
}

func TestEnsure_EnrollPersistRedial(t *testing.T) {
	ca := newTestCA(t)
	addr, core := startFakeCore(t, ca, "tok-1")
	data := t.TempDir()
	e := newEnv(map[string]string{
		EnvModuleID:       "media-movies",
		EnvGRPCAddr:       addr,
		EnvBootstrapToken: "tok-1",
		EnvTLSCA:          ca.caFile,
		EnvDataDir:        data,
		EnvEnrollDNSNames: "movies.svc, ",
	})
	ctx := context.Background()

	p, err := Ensure(ctx, Config{Getenv: e.get, Setenv: e.set})
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	dir := filepath.Join(data, DefaultDirName)
	if !p.Enrolled || p.Cert != filepath.Join(dir, CertFile) || p.Key != filepath.Join(dir, KeyFile) || p.CA != ca.caFile {
		t.Fatalf("paths = %+v", p)
	}
	if core.sawCert.Load() {
		t.Fatal("enrollment presented a client certificate")
	}
	core.mu.Lock()
	if len(core.lastNames) != 1 || core.lastNames[0] != "movies.svc" {
		t.Fatalf("requested DNS names = %v", core.lastNames)
	}
	core.mu.Unlock()
	if got := perm(t, dir); got != 0o700 {
		t.Fatalf("dir mode %v", got)
	}
	for _, f := range []string{CertFile, KeyFile, CAFile} {
		if got := perm(t, filepath.Join(dir, f)); got != 0o600 {
			t.Fatalf("%s mode %v", f, got)
		}
	}
	if e.get(EnvTLSCert) != p.Cert || e.get(EnvTLSKey) != p.Key || e.get(EnvTLSCA) != p.CA {
		t.Fatalf("env not exported: %v", e.m)
	}

	// Redial with the stored identity: core sees the verified module ID.
	pair, err := tls.LoadX509KeyPair(p.Cert, p.Key)
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM(ca.pemCA)
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(credentials.NewTLS(&tls.Config{
		Certificates: []tls.Certificate{pair}, RootCAs: pool, MinVersion: tls.VersionTLS12,
	})))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	resp, err := modulev1.NewModuleRegistrationClient(conn).Register(ctx, &modulev1.RegisterRequest{ModuleId: "media-movies"})
	if err != nil || !resp.Accepted || resp.NodeId != "media-movies" {
		t.Fatalf("redial: %v %+v", err, resp)
	}

	// Second start: reuses the files without contacting core (the token is
	// spent and not even needed).
	e2 := newEnv(map[string]string{EnvModuleID: "media-movies", EnvGRPCAddr: addr, EnvDataDir: data})
	p2, err := Ensure(ctx, Config{Getenv: e2.get, Setenv: e2.set})
	if err != nil {
		t.Fatalf("second Ensure: %v", err)
	}
	if p2.Enrolled || p2.Cert != p.Cert || p2.CA != filepath.Join(dir, CAFile) {
		t.Fatalf("second paths = %+v", p2)
	}
	if n := core.calls.Load(); n != 1 {
		t.Fatalf("core contacted %d times, want 1", n)
	}
	if e2.get(EnvTLSCert) != p.Cert {
		t.Fatal("reuse did not export env")
	}

	// A stored identity for another module is refused.
	e3 := newEnv(map[string]string{EnvDataDir: data})
	if _, err := Ensure(ctx, Config{ModuleID: "other", Getenv: e3.get, Setenv: e3.set}); err == nil || !strings.Contains(err.Error(), "media-movies") {
		t.Fatalf("CN mismatch: %v", err)
	}
}

func TestEnsure_RejectedTokenWritesNothing(t *testing.T) {
	ca := newTestCA(t)
	addr, _ := startFakeCore(t, ca, "right")
	dir := filepath.Join(t.TempDir(), "id")
	_, err := Ensure(context.Background(), Config{
		ModuleID: "m1", GRPCAddr: addr, Token: "wrong", CAFile: ca.caFile, Dir: dir,
		Getenv: newEnv(nil).get, Setenv: newEnv(nil).set,
	})
	if err == nil || !strings.Contains(err.Error(), "rejected") {
		t.Fatalf("want rejection, got %v", err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("identity dir created after rejection: %v", err)
	}
}

func TestEnsure_WrongCAFails(t *testing.T) {
	ca := newTestCA(t)
	other := newTestCA(t)
	addr, core := startFakeCore(t, ca, "tok")
	_, err := Ensure(context.Background(), Config{
		ModuleID: "m1", GRPCAddr: addr, Token: "tok", CAFile: other.caFile, Dir: t.TempDir() + "/id",
		Timeout: 2 * time.Second, Getenv: newEnv(nil).get, Setenv: newEnv(nil).set,
	})
	if err == nil {
		t.Fatal("enrolled against a core the CA does not vouch for")
	}
	if core.calls.Load() != 0 {
		t.Fatal("token sent to an unverified server")
	}
}

func TestEnsure_NoIdentity(t *testing.T) {
	_, err := Ensure(context.Background(), Config{ModuleID: "m1", Dir: t.TempDir(), Getenv: newEnv(nil).get, Setenv: newEnv(nil).set})
	if !errors.Is(err, ErrNoIdentity) {
		t.Fatalf("want ErrNoIdentity, got %v", err)
	}
	_, err = Ensure(context.Background(), Config{ModuleID: "m1", Token: "t", Dir: t.TempDir(), Getenv: newEnv(nil).get, Setenv: newEnv(nil).set})
	if err == nil || !strings.Contains(err.Error(), EnvTLSCA) {
		t.Fatalf("token without CA: %v", err)
	}
}

func TestEnsure_ExplicitCertPassThrough(t *testing.T) {
	e := newEnv(map[string]string{EnvTLSCert: "/x/c.pem", EnvTLSKey: "/x/k.pem", EnvTLSCA: "/x/ca.pem"})
	p, err := Ensure(context.Background(), Config{Getenv: e.get, Setenv: e.set})
	if err != nil || p.Cert != "/x/c.pem" || p.Key != "/x/k.pem" || p.CA != "/x/ca.pem" || p.Enrolled {
		t.Fatalf("passthrough: %+v %v", p, err)
	}
}

func TestEnsure_CAFromExportDir(t *testing.T) {
	ca := newTestCA(t)
	addr, _ := startFakeCore(t, ca, "tok")
	exportDir := filepath.Dir(ca.caFile)
	dir := filepath.Join(t.TempDir(), "id")
	e := newEnv(map[string]string{EnvCAExportDir: exportDir})
	p, err := Ensure(context.Background(), Config{ModuleID: "m1", GRPCAddr: addr, Token: "tok", Dir: dir, Getenv: e.get, Setenv: e.set})
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	// The pinned CA is stored with the identity and exported from there.
	if p.CA != filepath.Join(dir, CAFile) || e.get(EnvTLSCA) != p.CA {
		t.Fatalf("CA path = %q env=%q", p.CA, e.get(EnvTLSCA))
	}
}

func TestEnsure_ProfileGuard(t *testing.T) {
	for _, tc := range []struct {
		name    string
		env     map[string]string
		cfgInsc bool
		wantErr bool
	}{
		{"household + insecure flag", map[string]string{envProfile: "household", envInsecure: "true"}, false, true},
		{"staging + insecure config", map[string]string{envProfile: "staging"}, true, true},
		{"dev + insecure", map[string]string{envProfile: "dev", envInsecure: "1"}, false, false},
		{"unset + insecure infers dev", map[string]string{envInsecure: "true"}, false, false},
		{"unset + insecure config infers dev", nil, true, false},
		{"legacy flag", map[string]string{envProfile: "HOUSEHOLD", envInsecureLegacy: "1"}, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(tc.env)
			p, err := Ensure(context.Background(), Config{Insecure: tc.cfgInsc, Getenv: e.get, Setenv: e.set})
			if tc.wantErr {
				if !errors.Is(err, ErrInsecureInHousehold) {
					t.Fatalf("want ErrInsecureInHousehold, got %v", err)
				}
				return
			}
			if err != nil || p != (Paths{}) {
				t.Fatalf("dev insecure must be a no-op: %+v %v", p, err)
			}
		})
	}
}
