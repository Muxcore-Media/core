package mgr

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Muxcore-Media/core/internal/registry"
	"github.com/Muxcore-Media/core/pkg/contracts"
	modulev1 "github.com/Muxcore-Media/core/proto/gen/muxcore/module/v1"
)

func clearSpoolSigEnv(t *testing.T) {
	t.Helper()
	t.Setenv("MUXCORE_SPOOL_REQUIRE_SIGNATURE", "")
	t.Setenv("MUXCORE_SPOOL_PUBLIC_KEY", "")
	t.Setenv("MUXCORE_SPOOL_TRUSTED_KEYS_DIR", "")
}

func TestVerifyMarketplaceSignature_HouseholdRequires(t *testing.T) {
	clearSpoolSigEnv(t)
	m := NewManager("addr", nil, nil)
	bin := &ModuleBinary{ID: "mod", Path: filepath.Join(t.TempDir(), "x")}
	if err := os.WriteFile(bin.Path, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	// dev (default): signatures stay opt-in.
	if err := m.VerifyMarketplaceSignature(bin, ""); err != nil {
		t.Fatalf("dev marketplace deploy without signature: %v", err)
	}

	m.SetRequireMarketplaceSignatures(true)
	if !m.RequireMarketplaceSignatures() {
		t.Fatal("flag not stored")
	}
	if err := m.VerifyMarketplaceSignature(bin, ""); err == nil || !strings.Contains(err.Error(), "signature required") {
		t.Fatalf("household marketplace deploy without signature must fail, got %v", err)
	}
	// Boot-time curated tags stay checksum-anchored: VerifySignature is unaffected.
	if err := m.VerifySignature(bin, ""); err != nil {
		t.Fatalf("boot-time verification must not require signatures: %v", err)
	}
	if err := m.VerifyMarketplaceSignature(nil, ""); err == nil {
		t.Fatal("nil binary must fail")
	}
}

func TestResurrectOrphan_HouseholdRequiresSignature(t *testing.T) {
	clearSpoolSigEnv(t)
	m := NewManager("addr", registry.New(), nil)
	m.SetRequireMarketplaceSignatures(true)
	cacheRoot := t.TempDir()
	m.cacheDir = cacheRoot
	repo := "https://github.com/Muxcore-Media/orphan-unsigned"
	id := ModuleIDFromRepo(repo)
	binPath := filepath.Join(cacheRoot, id, "v1.0.0", "muxcore-module")
	if err := os.MkdirAll(filepath.Dir(binPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(binPath, []byte("binary"), 0o700); err != nil {
		t.Fatal(err)
	}
	m.SetTag(&contracts.TagDefinition{Modules: []contracts.TagModule{{Repo: repo, Version: "v1.0.0"}}})
	err := m.ResurrectOrphan(context.Background(), id)
	if err == nil || !strings.Contains(err.Error(), "signature orphan") {
		t.Fatalf("expected signature failure for unsigned orphan, got %v", err)
	}
}

// memIssuer issues in memory and fails the test if a file-based path is used.
type memIssuer struct {
	fileCertIssuer
	t *testing.T
}

func (mi memIssuer) IssueModuleCertForDir(string, string) (string, string, error) {
	mi.t.Error("BootstrapRegister must not write key files when the issuer supports in-memory issuance")
	return "", "", errors.New("unexpected")
}

func (mi memIssuer) IssueModuleCert(moduleID string, _ []net.IP, dns []string) ([]byte, []byte, error) {
	if moduleID != mi.moduleID {
		return nil, nil, errors.New("wrong module")
	}
	found := false
	for _, d := range dns {
		if d == moduleID {
			found = true
		}
	}
	if !found {
		mi.t.Errorf("module ID %q missing from DNS SANs %v", moduleID, dns)
	}
	return []byte("MEMCERT"), []byte("MEMKEY"), nil
}

func TestBootstrapRegister_KeyInMemory(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	m, _, _ := testLifecycleMgr(t)
	m.SetCertAuthority(memIssuer{fileCertIssuer: fileCertIssuer{moduleID: "m1"}, t: t})
	srv := &registrationServer{mgr: m}
	resp, err := srv.BootstrapRegister(context.Background(), &modulev1.BootstrapRegisterRequest{ModuleId: "m1", Token: "tok"})
	if err != nil || !resp.Accepted {
		t.Fatalf("bootstrap: %v %+v", err, resp)
	}
	if resp.SignedCert != "MEMCERT" || resp.KeyPem != "MEMKEY" {
		t.Fatalf("unexpected payload %q/%q", resp.SignedCert, resp.KeyPem)
	}
	assertEmptyDir(t, tmp)
}

func TestBootstrapRegister_FileIssuerLeavesNoKey(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	m, _, _ := testLifecycleMgr(t)
	m.SetCertAuthority(fileCertIssuer{moduleID: "m1"})
	srv := &registrationServer{mgr: m}
	resp, err := srv.BootstrapRegister(context.Background(), &modulev1.BootstrapRegisterRequest{ModuleId: "m1", Token: "tok"})
	if err != nil || !resp.Accepted || resp.KeyPem != "KEY" {
		t.Fatalf("bootstrap: %v %+v", err, resp)
	}
	assertEmptyDir(t, tmp)
}

func assertEmptyDir(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		t.Errorf("leftover in temp dir after BootstrapRegister: %s", e.Name())
	}
}
