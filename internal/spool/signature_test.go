package spool

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/crypto/blake2b"
)

func TestVerifyArtifactSignature_FixtureKeypair(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	artifact := filepath.Join(dir, "module.bin")
	payload := []byte("muxcore-module-artifact-v1")
	if err := os.WriteFile(artifact, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	sig := ed25519.Sign(priv, payload)
	keyPath := filepath.Join(dir, "spool.pub")
	if err := os.WriteFile(keyPath, []byte(hex.EncodeToString(pub)), 0o600); err != nil {
		t.Fatal(err)
	}

	t.Setenv("MUXCORE_SPOOL_REQUIRE_SIGNATURE", "1")
	t.Setenv("MUXCORE_SPOOL_PUBLIC_KEY", keyPath)
	t.Setenv("MUXCORE_SPOOL_TRUSTED_KEYS_DIR", "")

	if err := VerifyArtifactSignature(artifact, hex.EncodeToString(sig)); err != nil {
		t.Fatalf("inline sig: %v", err)
	}

	if err := os.WriteFile(artifact+".sig", []byte(hex.EncodeToString(sig)), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := VerifyArtifactSignature(artifact, ""); err != nil {
		t.Fatalf("sidecar sig: %v", err)
	}

	bad := make([]byte, len(sig))
	copy(bad, sig)
	bad[0] ^= 0xff
	if err := VerifyArtifactSignature(artifact, hex.EncodeToString(bad)); err == nil {
		t.Fatal("expected bad signature error")
	}
}

func TestVerifyArtifactSignature_RequireMissingFails(t *testing.T) {
	dir := t.TempDir()
	artifact := filepath.Join(dir, "mod.bin")
	_ = os.WriteFile(artifact, []byte("x"), 0o600)
	keyPath := filepath.Join(dir, "k.pub")
	pub, _, _ := ed25519.GenerateKey(nil)
	_ = os.WriteFile(keyPath, pub, 0o600)

	t.Setenv("MUXCORE_SPOOL_REQUIRE_SIGNATURE", "1")
	t.Setenv("MUXCORE_SPOOL_PUBLIC_KEY", keyPath)
	t.Setenv("MUXCORE_SPOOL_TRUSTED_KEYS_DIR", "")
	if err := VerifyArtifactSignature(artifact, ""); err == nil {
		t.Fatal("expected missing signature error")
	}
}

func TestVerifyArtifactSignature_OptionalSkip(t *testing.T) {
	t.Setenv("MUXCORE_SPOOL_REQUIRE_SIGNATURE", "")
	t.Setenv("MUXCORE_SPOOL_PUBLIC_KEY", "")
	t.Setenv("MUXCORE_SPOOL_TRUSTED_KEYS_DIR", "")
	dir := t.TempDir()
	artifact := filepath.Join(dir, "mod.bin")
	_ = os.WriteFile(artifact, []byte("x"), 0o600)
	if err := VerifyArtifactSignature(artifact, ""); err != nil {
		t.Fatal(err)
	}
}

func TestParseMinisignPublicKeyRoundTrip(t *testing.T) {
	pub, _, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	blob := make([]byte, 10+len(pub))
	blob[0], blob[1] = 'E', 'd'
	copy(blob[10:], pub)
	text := "untrusted comment: minisign public key ABC\n" + base64.StdEncoding.EncodeToString(blob) + "\n"
	got, err := ParsePublicKey([]byte(text))
	if err != nil {
		t.Fatal(err)
	}
	if !ed25519.PublicKey(got).Equal(pub) {
		t.Fatal("pubkey mismatch")
	}
}

func TestParseMinisigSignature(t *testing.T) {
	sig := make([]byte, ed25519.SignatureSize)
	for i := range sig {
		sig[i] = byte(i)
	}
	blob := make([]byte, 10+len(sig))
	blob[0], blob[1] = 'E', 'd'
	copy(blob[10:], sig)
	global := make([]byte, ed25519.SignatureSize)
	for i := range global {
		global[i] = byte(255 - i)
	}
	raw := "untrusted comment: signature from minisign key\n" +
		base64.StdEncoding.EncodeToString(blob) + "\n" +
		"trusted comment: timestamp:1\n" +
		base64.StdEncoding.EncodeToString(global) + "\n"
	got, err := parseMinisigSignature([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != ed25519.SignatureSize || got[3] != 3 {
		t.Fatalf("got %x", got)
	}
}

func TestVerifyArtifactSignature_MinisignHashedMode(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	artifact := filepath.Join(dir, "module.bin")
	payload := []byte("muxcore-minisign-hashed-fixture")
	if err := os.WriteFile(artifact, payload, 0o600); err != nil {
		t.Fatal(err)
	}

	h, err := blake2b.New512(nil)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = h.Write(payload)
	digest := h.Sum(nil)
	sig := ed25519.Sign(priv, digest)
	trusted := "timestamp:1700000000 file:module.bin"
	global := ed25519.Sign(priv, append(sig, []byte(trusted)...))

	blob := make([]byte, 10+len(sig))
	blob[0], blob[1] = 'E', 'D' // hashed mode
	copy(blob[2:10], []byte{1, 2, 3, 4, 5, 6, 7, 8})
	copy(blob[10:], sig)
	minisig := "untrusted comment: signature from muxcore test key\n" +
		base64.StdEncoding.EncodeToString(blob) + "\n" +
		"trusted comment: " + trusted + "\n" +
		base64.StdEncoding.EncodeToString(global) + "\n"
	if err := os.WriteFile(artifact+".minisig", []byte(minisig), 0o600); err != nil {
		t.Fatal(err)
	}

	keyPath := filepath.Join(dir, "minisign.pub")
	pubBlob := make([]byte, 10+len(pub))
	pubBlob[0], pubBlob[1] = 'E', 'd'
	copy(pubBlob[2:10], blob[2:10])
	copy(pubBlob[10:], pub)
	pubText := "untrusted comment: minisign public key TEST\n" + base64.StdEncoding.EncodeToString(pubBlob) + "\n"
	if err := os.WriteFile(keyPath, []byte(pubText), 0o600); err != nil {
		t.Fatal(err)
	}

	t.Setenv("MUXCORE_SPOOL_REQUIRE_SIGNATURE", "1")
	t.Setenv("MUXCORE_SPOOL_PUBLIC_KEY", keyPath)
	t.Setenv("MUXCORE_SPOOL_TRUSTED_KEYS_DIR", "")

	if err := VerifyArtifactSignature(artifact, ""); err != nil {
		t.Fatalf("hashed minisig: %v", err)
	}

	// Corrupt trusted comment → global sig fails.
	bad := strings.Replace(minisig, trusted, trusted+"x", 1)
	_ = os.WriteFile(artifact+".minisig", []byte(bad), 0o600)
	if err := VerifyArtifactSignature(artifact, ""); err == nil {
		t.Fatal("expected global signature failure")
	}
}

func TestVerifyArtifactSignature_MinisignLegacyMode(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	artifact := filepath.Join(dir, "module.bin")
	payload := []byte("muxcore-minisign-legacy-fixture")
	if err := os.WriteFile(artifact, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	sig := ed25519.Sign(priv, payload)
	trusted := "timestamp:1"
	global := ed25519.Sign(priv, append(sig, []byte(trusted)...))
	blob := make([]byte, 10+len(sig))
	blob[0], blob[1] = 'E', 'd'
	copy(blob[10:], sig)
	minisig := "untrusted comment: signature from muxcore test key\n" +
		base64.StdEncoding.EncodeToString(blob) + "\n" +
		"trusted comment: " + trusted + "\n" +
		base64.StdEncoding.EncodeToString(global) + "\n"
	_ = os.WriteFile(artifact+".minisig", []byte(minisig), 0o600)

	keysDir := filepath.Join(dir, "trusted")
	_ = os.MkdirAll(keysDir, 0o700)
	_ = os.WriteFile(filepath.Join(keysDir, "a.pub"), []byte(hex.EncodeToString(pub)), 0o600)

	t.Setenv("MUXCORE_SPOOL_REQUIRE_SIGNATURE", "1")
	t.Setenv("MUXCORE_SPOOL_PUBLIC_KEY", "")
	t.Setenv("MUXCORE_SPOOL_TRUSTED_KEYS_DIR", keysDir)

	if err := VerifyArtifactSignature(artifact, ""); err != nil {
		t.Fatalf("legacy minisig via trusted dir: %v", err)
	}
}

func TestCheckPublisherAllowlist(t *testing.T) {
	t.Setenv("MUXCORE_SPOOL_ALLOWED_PUBLISHERS", "")
	if err := CheckPublisherAllowlist(""); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MUXCORE_SPOOL_ALLOWED_PUBLISHERS", "MuxCore, Partner")
	if err := CheckPublisherAllowlist("muxcore"); err != nil {
		t.Fatal(err)
	}
	if err := CheckPublisherAllowlist(""); err == nil {
		t.Fatal("empty publisher should fail when allowlist set")
	}
	if err := CheckPublisherAllowlist("evil"); err == nil {
		t.Fatal("unexpected publisher should fail")
	}
}

func TestVerifyArtifactSignatureRequired(t *testing.T) {
	t.Setenv("MUXCORE_SPOOL_REQUIRE_SIGNATURE", "")
	t.Setenv("MUXCORE_SPOOL_TRUSTED_KEYS_DIR", "")
	dir := t.TempDir()
	artifact := filepath.Join(dir, "mod.bin")
	payload := []byte("artifact")
	_ = os.WriteFile(artifact, payload, 0o600)

	// No keys, no signature: optional passes, required fails.
	t.Setenv("MUXCORE_SPOOL_PUBLIC_KEY", "")
	if err := VerifyArtifactSignatureRequired(artifact, "", false); err != nil {
		t.Fatalf("optional: %v", err)
	}
	if err := VerifyArtifactSignatureRequired(artifact, "", true); err == nil {
		t.Fatal("required without signature must fail")
	}

	pub, priv, _ := ed25519.GenerateKey(nil)
	keyPath := filepath.Join(dir, "k.pub")
	_ = os.WriteFile(keyPath, []byte(hex.EncodeToString(pub)), 0o600)
	sig := hex.EncodeToString(ed25519.Sign(priv, payload))

	// Signature but no trusted key: required fails (cannot verify).
	if err := VerifyArtifactSignatureRequired(artifact, sig, true); err == nil || !strings.Contains(err.Error(), "no public keys") {
		t.Fatalf("required without keys must fail, got %v", err)
	}
	t.Setenv("MUXCORE_SPOOL_PUBLIC_KEY", keyPath)
	if err := VerifyArtifactSignatureRequired(artifact, sig, true); err != nil {
		t.Fatalf("valid signature: %v", err)
	}
	if err := VerifyArtifactSignatureRequired(artifact, "", true); err == nil {
		t.Fatal("required with key but no signature must fail")
	}
}
