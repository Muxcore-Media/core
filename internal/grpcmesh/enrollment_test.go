package grpcmesh

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Muxcore-Media/core/internal/enroll"
	modulemgr "github.com/Muxcore-Media/core/internal/module/mgr"
	modulev1 "github.com/Muxcore-Media/core/proto/gen/muxcore/module/v1"
	"google.golang.org/grpc/codes"
)

const testEnrollSecret = "0123456789abcdef-test-secret"

// moduleCSR returns a fresh module key and a PEM CSR for it. The CSR's own
// subject/SANs are deliberately wrong: core must ignore them.
func moduleCSR(t *testing.T) (*ecdsa.PrivateKey, string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{
		Subject:  pkix.Name{CommonName: "muxcored"},
		DNSNames: []string{"evil.example"},
	}, key)
	if err != nil {
		t.Fatal(err)
	}
	return key, string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: der}))
}

func startEnrollServer(t *testing.T, ca *CertAuthority) modulev1.ModuleRegistrationClient {
	t.Helper()
	addr, _ := startRegistrationServer(t, ca, modulemgr.RegistrationPolicy{Household: true},
		func(m *modulemgr.Manager) { m.SetCertAuthority(ca) })
	return registrationClient(t, addr, clientTLSCreds(t, ca, nil))
}

func bootstrap(t *testing.T, c modulev1.ModuleRegistrationClient, req *modulev1.BootstrapRegisterRequest) *modulev1.BootstrapRegisterResponse {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	resp, err := c.BootstrapRegister(ctx, req)
	if err != nil {
		t.Fatalf("BootstrapRegister RPC: %v", err)
	}
	return resp
}

func parseLeaf(t *testing.T, certPEM string) *x509.Certificate {
	t.Helper()
	block, _ := pem.Decode([]byte(certPEM))
	if block == nil {
		t.Fatal("no PEM certificate in response")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	return cert
}

// ADR-0017: a module enrolls over server-verified TLS (no client cert),
// core signs its CSR with CN = module ID and policy SANs, and the module can
// then register a security capability over mTLS with its own key.
func TestEnrollment_SignsCSRAndRegisters(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	ca := testCA(t)
	if err := ca.ConfigureEnrollment(testEnrollSecret, "auth-local.svc, *.lan"); err != nil {
		t.Fatal(err)
	}
	c := startEnrollServer(t, ca)
	key, csrPEM := moduleCSR(t)

	resp := bootstrap(t, c, &modulev1.BootstrapRegisterRequest{
		Token:    enroll.Token([]byte(testEnrollSecret), "auth-local"),
		ModuleId: "auth-local",
		CsrPem:   csrPEM,
		DnsNames: []string{"auth-local.svc", "auth-local.home.lan", "evil.example", "10.0.0.9"},
	})
	if !resp.Accepted {
		t.Fatalf("enrollment rejected: %s", resp.Error)
	}
	if resp.KeyPem != "" {
		t.Fatal("core returned a private key for a CSR enrollment")
	}
	if !bytes.Equal([]byte(resp.CaCert), ca.CACertPEM()) {
		t.Fatal("response CA differs from the core CA")
	}
	leaf := parseLeaf(t, resp.SignedCert)
	if leaf.Subject.CommonName != "auth-local" {
		t.Fatalf("CN = %q", leaf.Subject.CommonName)
	}
	if !leaf.PublicKey.(*ecdsa.PublicKey).Equal(&key.PublicKey) {
		t.Fatal("certificate is not for the module's key")
	}
	wantDNS := []string{"localhost", "auth-local", "auth-local.svc", "auth-local.home.lan"}
	if !slices.Equal(leaf.DNSNames, wantDNS) {
		t.Fatalf("DNS SANs = %v, want %v", leaf.DNSNames, wantDNS)
	}
	var ips []string
	for _, ip := range leaf.IPAddresses {
		ips = append(ips, ip.String())
	}
	if !slices.Equal(ips, []string{"127.0.0.1", "::1"}) {
		t.Fatalf("IP SANs = %v (10.0.0.9 is not allowed)", ips)
	}
	if _, err := leaf.Verify(x509.VerifyOptions{Roots: caPool(t, ca), KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}); err != nil {
		t.Fatalf("certificate does not verify against the core CA: %v", err)
	}

	// The enrolled identity is a verified module principal.
	tlsCert := tls.Certificate{Certificate: [][]byte{leaf.Raw}, PrivateKey: key, Leaf: leaf}
	addr, _ := startRegistrationServer(t, ca, modulemgr.RegistrationPolicy{Household: true})
	owner := registrationClient(t, addr, clientTLSCreds(t, ca, &tlsCert))
	if code := registerOver(t, owner, "auth-local", "auth", "identity"); code != codes.OK {
		t.Fatalf("enrolled module register: %v", code)
	}

	// Reuse is rejected.
	_, csr2 := moduleCSR(t)
	again := bootstrap(t, c, &modulev1.BootstrapRegisterRequest{
		Token: enroll.Token([]byte(testEnrollSecret), "auth-local"), ModuleId: "auth-local", CsrPem: csr2,
	})
	if again.Accepted || !strings.Contains(again.Error, "already used") {
		t.Fatalf("token reuse: accepted=%v err=%q", again.Accepted, again.Error)
	}

	assertNoModuleKeys(t, ca.Dir())
	assertNoEntries(t, tmp)
}

func TestEnrollment_TokenForAnotherModule(t *testing.T) {
	ca := testCA(t)
	if err := ca.ConfigureEnrollment(testEnrollSecret, ""); err != nil {
		t.Fatal(err)
	}
	c := startEnrollServer(t, ca)
	_, csrPEM := moduleCSR(t)
	tokA := enroll.Token([]byte(testEnrollSecret), "module-a")

	// A's token presented for B.
	resp := bootstrap(t, c, &modulev1.BootstrapRegisterRequest{Token: tokA, ModuleId: "module-b", CsrPem: csrPEM})
	if resp.Accepted || !strings.Contains(resp.Error, "module-a") {
		t.Fatalf("A's token for B: accepted=%v err=%q", resp.Accepted, resp.Error)
	}
	// A's MAC relabelled as B's token.
	forged := enroll.TokenPrefix + "module-b_" + tokA[len(tokA)-64:]
	resp = bootstrap(t, c, &modulev1.BootstrapRegisterRequest{Token: forged, ModuleId: "module-b", CsrPem: csrPEM})
	if resp.Accepted || !strings.Contains(resp.Error, "invalid") {
		t.Fatalf("forged token: accepted=%v err=%q", resp.Accepted, resp.Error)
	}
	// A version-2 token without a CSR is rejected.
	resp = bootstrap(t, c, &modulev1.BootstrapRegisterRequest{Token: tokA, ModuleId: "module-a"})
	if resp.Accepted || !strings.Contains(resp.Error, "csr_pem") {
		t.Fatalf("v2 token without CSR: accepted=%v err=%q", resp.Accepted, resp.Error)
	}
	// A malformed CSR is rejected.
	resp = bootstrap(t, c, &modulev1.BootstrapRegisterRequest{Token: tokA, ModuleId: "module-a", CsrPem: "junk"})
	if resp.Accepted {
		t.Fatal("malformed CSR accepted")
	}
	// None of the rejected attempts consumed A's token.
	resp = bootstrap(t, c, &modulev1.BootstrapRegisterRequest{Token: tokA, ModuleId: "module-a", CsrPem: csrPEM})
	if !resp.Accepted {
		t.Fatalf("A's own enrollment after rejected attempts: %s", resp.Error)
	}
}

// The ledger survives a core restart (a new CertAuthority over the same CA
// directory), and `enroll reset` re-arms the token on a running core.
func TestEnrollment_LedgerPersistsAcrossRestart(t *testing.T) {
	dir := t.TempDir()
	ca1, err := NewCertAuthority(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := ca1.ConfigureEnrollment(testEnrollSecret, ""); err != nil {
		t.Fatal(err)
	}
	tok := enroll.Token([]byte(testEnrollSecret), "media-movies")
	if id, err := ca1.ValidateToken(tok); err != nil || id != "media-movies" {
		t.Fatalf("first use: %q %v", id, err)
	}
	ca1.Close()

	info, err := os.Stat(filepath.Join(dir, enroll.LedgerFile))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("ledger mode = %v", info.Mode().Perm())
	}

	ca2, err := NewCertAuthority(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(ca2.Close)
	if err := ca2.ConfigureEnrollment(testEnrollSecret, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := ca2.ValidateToken(tok); err == nil {
		t.Fatal("token accepted again after restart")
	}
	if removed, err := enroll.NewLedger(dir).Reset("media-movies"); err != nil || !removed {
		t.Fatalf("reset: %v %v", removed, err)
	}
	if _, err := ca2.ValidateToken(tok); err != nil {
		t.Fatalf("token after reset: %v", err)
	}
}

// Without MUXCORE_ENROLL_SECRET only the in-memory version-1 tokens work.
func TestEnrollment_NoSecretKeepsInMemoryTokens(t *testing.T) {
	ca := testCA(t)
	if err := ca.ConfigureEnrollment("", ""); err != nil {
		t.Fatal(err)
	}
	c := startEnrollServer(t, ca)
	_, csrPEM := moduleCSR(t)

	resp := bootstrap(t, c, &modulev1.BootstrapRegisterRequest{
		Token: enroll.Token([]byte(testEnrollSecret), "m1"), ModuleId: "m1", CsrPem: csrPEM,
	})
	if resp.Accepted || !strings.Contains(resp.Error, enroll.EnvSecret) {
		t.Fatalf("v2 token without secret: accepted=%v err=%q", resp.Accepted, resp.Error)
	}

	// v1 token + CSR: signed, no key returned.
	tok, err := ca.GenerateToken("m1")
	if err != nil {
		t.Fatal(err)
	}
	resp = bootstrap(t, c, &modulev1.BootstrapRegisterRequest{Token: tok, ModuleId: "m1", CsrPem: csrPEM})
	if !resp.Accepted || resp.KeyPem != "" || parseLeaf(t, resp.SignedCert).Subject.CommonName != "m1" {
		t.Fatalf("v1 token with CSR: %+v", resp)
	}
	// v1 token without CSR: legacy in-memory key.
	tok, _ = ca.GenerateToken("m2")
	resp = bootstrap(t, c, &modulev1.BootstrapRegisterRequest{Token: tok, ModuleId: "m2"})
	if !resp.Accepted || !strings.Contains(resp.KeyPem, "PRIVATE KEY") {
		t.Fatalf("v1 legacy: accepted=%v err=%q", resp.Accepted, resp.Error)
	}
	if _, err := os.Stat(filepath.Join(ca.Dir(), enroll.LedgerFile)); !os.IsNotExist(err) {
		t.Fatalf("ledger written without a secret: %v", err)
	}
}

func TestConfigureEnrollment_ShortSecret(t *testing.T) {
	ca := testCA(t)
	if err := ca.ConfigureEnrollment("short", ""); err == nil {
		t.Fatal("short secret accepted")
	}
}

// assertNoModuleKeys fails if the CA directory holds a private key other
// than the CA's own.
func assertNoModuleKeys(t *testing.T, dir string) {
	t.Helper()
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(dir, path)
		allowed := map[string]bool{"ca.crt": true, "ca.key": true, enroll.LedgerFile: true, enroll.LedgerFile + ".lock": true}
		if !allowed[rel] {
			t.Errorf("unexpected file in CA dir after enrollment: %s", rel)
		}
		if rel != "ca.key" {
			data, _ := os.ReadFile(path) //nolint:gosec // test temp dir
			if bytes.Contains(data, []byte("PRIVATE KEY")) {
				t.Errorf("private key material in %s", rel)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func assertNoEntries(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		t.Errorf("leftover in %s: %s", dir, e.Name())
	}
}
