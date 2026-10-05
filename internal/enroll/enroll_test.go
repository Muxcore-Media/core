package enroll

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
)

var secret = []byte("0123456789abcdef-secret")

func TestTokenRoundTrip(t *testing.T) {
	for _, id := range []string{"auth-local", "my_module", "a.b-c_d"} {
		tok := Token(secret, id)
		got, err := VerifyToken(secret, tok)
		if err != nil || got != id {
			t.Fatalf("%s: VerifyToken = %q, %v", id, got, err)
		}
		if gotID, ok := TokenModuleID(tok); !ok || gotID != id {
			t.Fatalf("%s: TokenModuleID = %q %v", id, gotID, ok)
		}
	}
	if Token(secret, "a") == Token([]byte("another-secret-value"), "a") {
		t.Fatal("token does not depend on the secret")
	}
}

func TestVerifyTokenRejects(t *testing.T) {
	good := Token(secret, "module-a")
	cases := map[string]string{
		"other secret":   Token([]byte("xxxxxxxxxxxxxxxxxxxx"), "module-a"),
		"relabelled":     TokenPrefix + "module-b_" + good[len(good)-macHexLen:],
		"v1 prefix":      "mct_1_module-a_" + good[len(good)-macHexLen:],
		"short mac":      good[:len(good)-2],
		"non-hex mac":    good[:len(good)-1] + "z",
		"no id":          TokenPrefix + "_" + good[len(good)-macHexLen:],
		"bad id charset": Token(secret, "bad id"),
	}
	for name, tok := range cases {
		if _, err := VerifyToken(secret, tok); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if _, err := VerifyToken(nil, good); !errors.Is(err, ErrNoSecret) {
		t.Errorf("no secret: %v", err)
	}
}

func TestVerifierSingleUseAndReset(t *testing.T) {
	dir := t.TempDir()
	v, err := NewVerifier(string(secret), NewLedger(dir))
	if err != nil {
		t.Fatal(err)
	}
	tok := Token(secret, "m1")
	if _, err := v.Validate(tok); err != nil {
		t.Fatal(err)
	}
	if _, err := v.Validate(tok); !errors.Is(err, ErrAlreadyEnrolled) {
		t.Fatalf("reuse: %v", err)
	}
	// A fresh ledger over the same dir (restart) still sees the entry.
	l2 := NewLedger(dir)
	if has, err := l2.Has("m1"); err != nil || !has {
		t.Fatalf("after restart: %v %v", has, err)
	}
	entries, err := l2.List()
	if err != nil || len(entries) != 1 || entries[0].ModuleID != "m1" || entries[0].EnrolledAt.IsZero() {
		t.Fatalf("List = %+v, %v", entries, err)
	}
	if removed, err := l2.Reset("m1"); err != nil || !removed {
		t.Fatalf("reset: %v %v", removed, err)
	}
	if removed, _ := l2.Reset("m1"); removed {
		t.Fatal("second reset removed something")
	}
	if _, err := v.Validate(tok); err != nil {
		t.Fatalf("after reset: %v", err)
	}
	info, err := os.Stat(filepath.Join(dir, LedgerFile))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("ledger perms: %v %v", info, err)
	}
	if matches, _ := filepath.Glob(filepath.Join(dir, LedgerFile+".tmp-*")); len(matches) != 0 {
		t.Fatalf("temp files left: %v", matches)
	}
}

func TestVerifierConcurrentSingleUse(t *testing.T) {
	v, err := NewVerifier(string(secret), NewLedger(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	tok := Token(secret, "race")
	var wg sync.WaitGroup
	var mu sync.Mutex
	ok := 0
	for range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := v.Validate(tok); err == nil {
				mu.Lock()
				ok++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if ok != 1 {
		t.Fatalf("%d concurrent validations succeeded, want 1", ok)
	}
}

func TestLedgerCorruptFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, LedgerFile), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := NewLedger(dir).Consume("m1"); err == nil {
		t.Fatal("corrupt ledger accepted (must fail closed)")
	}
}

func TestNewVerifierShortSecret(t *testing.T) {
	if _, err := NewVerifier("short", NewLedger(t.TempDir())); err == nil {
		t.Fatal("short secret accepted")
	}
}

func TestSANPolicy(t *testing.T) {
	var none SANPolicy
	ips, dns, dropped := none.SANs("mod", []string{"mod.svc", "localhost", "mod"})
	if !slices.Equal(dns, []string{"localhost", "mod"}) || len(ips) != 2 || !slices.Equal(dropped, []string{"mod.svc"}) {
		t.Fatalf("default policy: ips=%v dns=%v dropped=%v", ips, dns, dropped)
	}

	p := ParseSANAllow(" Exact.Host , *.lan,10.0.0.5, ")
	for name, want := range map[string]bool{
		"exact.host":   true,
		"EXACT.HOST.":  true,
		"x.exact.host": false,
		"a.lan":        true,
		"a.b.lan":      true,
		"lan":          false,
		".lan":         false,
		"10.0.0.5":     true,
		"10.0.0.6":     false,
		"evil.com":     false,
		"":             false,
	} {
		if got := p.Allowed(name); got != want {
			t.Errorf("Allowed(%q) = %v, want %v", name, got, want)
		}
	}
	ips, dns, dropped = p.SANs("mod", []string{"svc.lan", "10.0.0.5", "evil.com", "SVC.lan"})
	if !slices.Equal(dns, []string{"localhost", "mod", "svc.lan"}) {
		t.Fatalf("dns = %v", dns)
	}
	if len(ips) != 3 || ips[2].String() != "10.0.0.5" {
		t.Fatalf("ips = %v", ips)
	}
	if !slices.Equal(dropped, []string{"evil.com"}) {
		t.Fatalf("dropped = %v", dropped)
	}
	if ParseSANAllow("*.lan").Allowed("10.lan") != true || ParseSANAllow("*.0.5").Allowed("10.0.0.5") {
		t.Fatal("wildcards must match DNS names only")
	}
}

func csrPEM(t *testing.T, key any) string {
	t.Helper()
	der, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{}, key)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: der}))
}

func TestParseCSR(t *testing.T) {
	p256, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if _, err := ParseCSR(csrPEM(t, p256)); err != nil {
		t.Fatalf("P-256: %v", err)
	}
	p224, _ := ecdsa.GenerateKey(elliptic.P224(), rand.Reader)
	if _, err := ParseCSR(csrPEM(t, p224)); err == nil {
		t.Fatal("P-224 accepted")
	}
	rsa1024, _ := rsa.GenerateKey(rand.Reader, 1024) //nolint:gosec // testing the size check
	if _, err := ParseCSR(csrPEM(t, rsa1024)); err == nil {
		t.Fatal("RSA-1024 accepted")
	}
	good := csrPEM(t, p256)
	block, _ := pem.Decode([]byte(good))
	block.Bytes[len(block.Bytes)-3] ^= 0xff
	if _, err := ParseCSR(string(pem.EncodeToMemory(block))); err == nil {
		t.Fatal("tampered CSR accepted")
	}
	for _, bad := range []string{"", "junk", string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: []byte{1}}))} {
		if _, err := ParseCSR(bad); err == nil {
			t.Fatalf("accepted %q", bad)
		}
	}
}
