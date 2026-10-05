package main

import (
	"crypto/x509"
	"encoding/pem"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/Muxcore-Media/core/internal/config"
	"github.com/Muxcore-Media/core/internal/profile"
)

func envOf(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func secTestConfig() *config.Config {
	cfg := config.Default()
	cfg.GRPC.Addr = "127.0.0.1:0"
	return cfg
}

func TestSetupMeshSecurity_HouseholdAutoCA(t *testing.T) {
	t.Setenv("HOME", t.TempDir()) // no legacy CA
	data := t.TempDir()
	export := filepath.Join(t.TempDir(), "export")
	env := envOf(map[string]string{
		profile.EnvDataDir:     data,
		profile.EnvCAExportDir: export,
		envTLSServerSANs:       "muxcore-core, 10.0.0.5",
	})
	cfg := secTestConfig()

	sec, err := setupMeshSecurity(cfg, profile.Resolved{Name: profile.Household}, env)
	if err != nil {
		t.Fatalf("setupMeshSecurity: %v", err)
	}
	t.Cleanup(sec.certAuth.Close)
	if sec.serverCreds == nil || sec.sidecar == nil || sec.certAuth == nil || !sec.autoCert {
		t.Fatalf("household must enable TLS with an auto CA: %+v", sec)
	}
	if sec.caDir != filepath.Join(data, "ca") {
		t.Fatalf("CA dir = %q, want <data>/ca", sec.caDir)
	}
	if _, err := os.Stat(filepath.Join(data, "ca", "ca.key")); err != nil {
		t.Fatalf("CA key not under data dir: %v", err)
	}
	exported, err := os.ReadFile(filepath.Join(export, "ca.crt"))
	if err != nil || string(exported) != string(sec.certAuth.CACertPEM()) {
		t.Fatalf("CA not exported: %v", err)
	}
	if cfg.GRPC.CertFile == "" || cfg.Server.CertFile != cfg.GRPC.CertFile {
		t.Fatalf("expected auto cert for gRPC and HTTP: grpc=%q http=%q", cfg.GRPC.CertFile, cfg.Server.CertFile)
	}
	pemData, err := os.ReadFile(cfg.GRPC.CertFile)
	if err != nil {
		t.Fatal(err)
	}
	block, _ := pem.Decode(pemData)
	leaf, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"localhost", "muxcore", "muxcored", "muxcore-core"} {
		if !slices.Contains(leaf.DNSNames, want) {
			t.Errorf("server cert missing DNS SAN %q: %v", want, leaf.DNSNames)
		}
	}
	foundIP := false
	for _, ip := range leaf.IPAddresses {
		if ip.String() == "10.0.0.5" {
			foundIP = true
		}
	}
	if !foundIP {
		t.Errorf("server cert missing IP SAN 10.0.0.5: %v", leaf.IPAddresses)
	}
}

func TestSetupMeshSecurity_HouseholdInsecureFatal(t *testing.T) {
	_, err := setupMeshSecurity(secTestConfig(), profile.Resolved{Name: profile.Household, Insecure: true}, envOf(nil))
	if !errors.Is(err, profile.ErrInsecureInHousehold) {
		t.Fatalf("expected ErrInsecureInHousehold, got %v", err)
	}
}

func TestSetupMeshSecurity_DevInsecurePlaintext(t *testing.T) {
	cfg := secTestConfig()
	sec, err := setupMeshSecurity(cfg, profile.Resolved{Name: profile.Dev, Insecure: true}, envOf(map[string]string{profile.EnvDataDir: t.TempDir()}))
	if err != nil {
		t.Fatal(err)
	}
	if sec.serverCreds != nil || sec.sidecar != nil || sec.certAuth != nil {
		t.Fatalf("dev+insecure without certs must be plaintext without CA: %+v", sec)
	}
	if cfg.Server.CertFile != "" {
		t.Fatal("HTTP cert must stay unset in plaintext mode")
	}
}

func TestSetupMeshSecurity_ExplicitMTLSKeepsRequire(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	data := t.TempDir()
	// First run issues a cert we then configure explicitly.
	cfg := secTestConfig()
	sec, err := setupMeshSecurity(cfg, profile.Resolved{Name: profile.Household}, envOf(map[string]string{profile.EnvDataDir: data}))
	if err != nil {
		t.Fatal(err)
	}
	sec.certAuth.Close()

	cfg2 := secTestConfig()
	cfg2.GRPC.CertFile, cfg2.GRPC.KeyFile = cfg.GRPC.CertFile, cfg.GRPC.KeyFile
	cfg2.GRPC.MTLSEnabled = true
	sec2, err := setupMeshSecurity(cfg2, profile.Resolved{Name: profile.Dev}, envOf(map[string]string{profile.EnvDataDir: data}))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(sec2.certAuth.Close)
	if sec2.autoCert || sec2.serverCreds == nil || sec2.certAuth == nil {
		t.Fatalf("explicit certs + mtls: %+v", sec2)
	}
	if sec2.serverCreds.Info().SecurityProtocol != "tls" {
		t.Fatal("expected TLS creds")
	}
}

func TestResolveCADir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	data := t.TempDir()
	env := envOf(map[string]string{profile.EnvDataDir: data})

	cfg := secTestConfig()
	cfg.GRPC.CACertDir = "/explicit"
	if got := resolveCADir(cfg, env); got != "/explicit" {
		t.Fatalf("explicit ca_cert_dir ignored: %q", got)
	}
	cfg.GRPC.CACertDir = ""
	if got := resolveCADir(cfg, env); got != filepath.Join(data, "ca") {
		t.Fatalf("default = %q", got)
	}
	legacy := filepath.Join(home, ".muxcore", "ca")
	if err := os.MkdirAll(legacy, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(legacy, "ca.crt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := resolveCADir(cfg, env); got != legacy {
		t.Fatalf("existing legacy CA must be kept: %q", got)
	}
	if err := os.MkdirAll(filepath.Join(data, "ca"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(data, "ca", "ca.crt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := resolveCADir(cfg, env); got != filepath.Join(data, "ca") {
		t.Fatalf("data-dir CA must win over legacy: %q", got)
	}
}

func TestResolveProfile(t *testing.T) {
	t.Setenv(profile.EnvProfile, "household")
	t.Setenv(profile.EnvInsecure, "true")
	t.Setenv(profile.EnvInsecureLegacy, "")
	if _, err := resolveProfile(); !errors.Is(err, profile.ErrInsecureInHousehold) {
		t.Fatalf("expected fatal insecure-in-household, got %v", err)
	}
	t.Setenv(profile.EnvProfile, "")
	p, err := resolveProfile()
	if err != nil || !p.IsDev() || p.Source != profile.SourceInferredInsecure {
		t.Fatalf("phase-0 inference: %+v %v", p, err)
	}
}
