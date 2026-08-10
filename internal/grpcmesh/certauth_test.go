package grpcmesh

import (
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCertAuthority_New(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "ca")
	ca, err := NewCertAuthority(dir)
	if err != nil {
		t.Fatalf("NewCertAuthority: %v", err)
	}
	defer ca.Close()

	for _, name := range []string{"ca.crt", "ca.key"} {
		info, err := os.Stat(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("stat %s: %v", name, err)
		}
		if info.Mode().Perm() != 0600 {
			t.Errorf("%s permissions = %o, want 0600", name, info.Mode().Perm())
		}
	}
}

func TestCertAuthority_LoadExisting(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "ca")
	ca1, err := NewCertAuthority(dir)
	if err != nil {
		t.Fatalf("first NewCertAuthority: %v", err)
	}
	pem1 := ca1.CACertPEM()
	ca1.Close()

	ca2, err := NewCertAuthority(dir)
	if err != nil {
		t.Fatalf("second NewCertAuthority: %v", err)
	}
	defer ca2.Close()

	pem2 := ca2.CACertPEM()
	if string(pem1) != string(pem2) {
		t.Fatal("reloaded CA cert differs from original")
	}
}

func TestCertAuthority_CACertPEM(t *testing.T) {
	ca, err := NewCertAuthority(filepath.Join(t.TempDir(), "ca"))
	if err != nil {
		t.Fatalf("NewCertAuthority: %v", err)
	}
	defer ca.Close()

	raw := ca.CACertPEM()
	if len(raw) == 0 {
		t.Fatal("CACertPEM returned empty")
	}
	block, _ := pem.Decode(raw)
	if block == nil {
		t.Fatal("CACertPEM is not valid PEM")
	}
	if block.Type != "CERTIFICATE" {
		t.Errorf("PEM type = %q, want CERTIFICATE", block.Type)
	}
}

func TestCertAuthority_IssueModuleCert(t *testing.T) {
	ca, err := NewCertAuthority(filepath.Join(t.TempDir(), "ca"))
	if err != nil {
		t.Fatalf("NewCertAuthority: %v", err)
	}
	defer ca.Close()

	certPEM, keyPEM, err := ca.IssueModuleCert("test-mod",
		[]net.IP{net.ParseIP("127.0.0.1")}, []string{"localhost"})
	if err != nil {
		t.Fatalf("IssueModuleCert: %v", err)
	}

	certBlock, _ := pem.Decode(certPEM)
	if certBlock == nil {
		t.Fatal("cert PEM decode failed")
	}
	keyBlock, _ := pem.Decode(keyPEM)
	if keyBlock == nil {
		t.Fatal("key PEM decode failed")
	}

	cert, err := x509.ParseCertificate(certBlock.Bytes)
	if err != nil {
		t.Fatalf("parse cert: %v", err)
	}
	if cert.Subject.CommonName != "test-mod" {
		t.Errorf("CN = %q, want %q", cert.Subject.CommonName, "test-mod")
	}

	caBlock, _ := pem.Decode(ca.CACertPEM())
	caCert, _ := x509.ParseCertificate(caBlock.Bytes)
	pool := x509.NewCertPool()
	pool.AddCert(caCert)
	if _, err := cert.Verify(x509.VerifyOptions{Roots: pool}); err != nil {
		t.Errorf("cert verification failed: %v", err)
	}
}

func TestCertAuthority_IssueModuleCertForDir(t *testing.T) {
	ca, err := NewCertAuthority(filepath.Join(t.TempDir(), "ca"))
	if err != nil {
		t.Fatalf("NewCertAuthority: %v", err)
	}
	defer ca.Close()

	modDir := filepath.Join(t.TempDir(), "mod")
	certPath, keyPath, err := ca.IssueModuleCertForDir("file-mod", modDir)
	if err != nil {
		t.Fatalf("IssueModuleCertForDir: %v", err)
	}

	for _, p := range []string{certPath, keyPath} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("stat %s: %v", p, err)
		}
	}
	if filepath.Base(certPath) != "module.crt" {
		t.Errorf("cert file = %q, want module.crt", filepath.Base(certPath))
	}
	if filepath.Base(keyPath) != "module.key" {
		t.Errorf("key file = %q, want module.key", filepath.Base(keyPath))
	}
}

func TestCertAuthority_GenerateToken(t *testing.T) {
	ca, err := NewCertAuthority(filepath.Join(t.TempDir(), "ca"))
	if err != nil {
		t.Fatalf("NewCertAuthority: %v", err)
	}
	defer ca.Close()

	tok, err := ca.GenerateToken("mod-x")
	if err != nil {
		t.Fatalf("GenerateToken: %v", err)
	}
	if !strings.HasPrefix(tok, tokenPrefix) {
		t.Errorf("token %q missing prefix %q", tok, tokenPrefix)
	}
	if !strings.Contains(tok, "mod-x") {
		t.Errorf("token %q missing module ID", tok)
	}
}

func TestCertAuthority_ValidateToken(t *testing.T) {
	ca, err := NewCertAuthority(filepath.Join(t.TempDir(), "ca"))
	if err != nil {
		t.Fatalf("NewCertAuthority: %v", err)
	}
	defer ca.Close()

	tok, err := ca.GenerateToken("mod-a")
	if err != nil {
		t.Fatalf("GenerateToken: %v", err)
	}

	got, err := ca.ValidateToken(tok)
	if err != nil {
		t.Fatalf("ValidateToken: %v", err)
	}
	if got != "mod-a" {
		t.Errorf("module ID = %q, want %q", got, "mod-a")
	}
}

func TestCertAuthority_ValidateToken_InvalidFormat(t *testing.T) {
	ca, err := NewCertAuthority(filepath.Join(t.TempDir(), "ca"))
	if err != nil {
		t.Fatalf("NewCertAuthority: %v", err)
	}
	defer ca.Close()

	bad := []string{
		"",
		"garbage",
		tokenPrefix + "nomoduleid",
	}
	for _, tok := range bad {
		if _, err := ca.ValidateToken(tok); err == nil {
			t.Errorf("ValidateToken(%q) should have failed", tok)
		}
	}
}

func TestCertAuthority_ValidateToken_SingleUse(t *testing.T) {
	ca, err := NewCertAuthority(filepath.Join(t.TempDir(), "ca"))
	if err != nil {
		t.Fatalf("NewCertAuthority: %v", err)
	}
	defer ca.Close()

	tok, _ := ca.GenerateToken("mod-b")
	if _, err := ca.ValidateToken(tok); err != nil {
		t.Fatalf("first ValidateToken: %v", err)
	}
	if _, err := ca.ValidateToken(tok); err == nil {
		t.Fatal("second ValidateToken should fail (already used)")
	}
}

func TestCertAuthority_ValidateToken_Expired(t *testing.T) {
	ca, err := NewCertAuthority(filepath.Join(t.TempDir(), "ca"))
	if err != nil {
		t.Fatalf("NewCertAuthority: %v", err)
	}
	defer ca.Close()

	tok, _ := ca.GenerateToken("mod-c")

	hash := sha256.Sum256([]byte(tok))
	key := hex.EncodeToString(hash[:])

	ca.mu.Lock()
	ca.tokens[key].expiresAt = time.Now().Add(-1 * time.Hour)
	ca.mu.Unlock()

	if _, err := ca.ValidateToken(tok); err == nil {
		t.Fatal("ValidateToken should fail for expired token")
	}
}

func TestCertAuthority_ValidateToken_MismatchedModule(t *testing.T) {
	ca, err := NewCertAuthority(filepath.Join(t.TempDir(), "ca"))
	if err != nil {
		t.Fatalf("NewCertAuthority: %v", err)
	}
	defer ca.Close()

	tok, _ := ca.GenerateToken("real-mod")

	hash := sha256.Sum256([]byte(tok))
	key := hex.EncodeToString(hash[:])

	ca.mu.Lock()
	ca.tokens[key].moduleID = "other-mod"
	ca.mu.Unlock()

	if _, err := ca.ValidateToken(tok); err == nil {
		t.Fatal("ValidateToken should fail for mismatched module ID")
	}
}

func TestMTLSConfig_NilCA(t *testing.T) {
	if cfg := MTLSConfig(nil); cfg != nil {
		t.Fatalf("MTLSConfig(nil) = %v, want nil", cfg)
	}
}

func TestMTLSConfig_ValidCA(t *testing.T) {
	ca, err := NewCertAuthority(filepath.Join(t.TempDir(), "ca"))
	if err != nil {
		t.Fatalf("NewCertAuthority: %v", err)
	}
	defer ca.Close()

	cfg := MTLSConfig(ca)
	if cfg == nil {
		t.Fatal("MTLSConfig returned nil for non-nil CA")
	}
	if cfg.ClientCAs == nil {
		t.Error("ClientCAs is nil")
	}
	if cfg.MinVersion != 0x0303 {
		t.Errorf("MinVersion = %#x, want 0x0303 (TLS 1.2)", cfg.MinVersion)
	}
}

func TestMTLSServerConfig_IssuesServerCert(t *testing.T) {
	ca, err := NewCertAuthority(filepath.Join(t.TempDir(), "ca"))
	if err != nil {
		t.Fatalf("NewCertAuthority: %v", err)
	}
	defer ca.Close()

	cfg, err := MTLSServerConfig(ca)
	if err != nil {
		t.Fatal(err)
	}
	if cfg == nil || len(cfg.Certificates) != 1 {
		t.Fatal("expected server certificate")
	}
	if cfg.ClientAuth != tls.RequireAndVerifyClientCert {
		t.Fatalf("ClientAuth=%v", cfg.ClientAuth)
	}
	if cfg.ClientCAs == nil {
		t.Fatal("expected ClientCAs")
	}
}

func TestCertAuthority_Close(t *testing.T) {
	ca, err := NewCertAuthority(filepath.Join(t.TempDir(), "ca"))
	if err != nil {
		t.Fatalf("NewCertAuthority: %v", err)
	}
	ca.Close()
}
