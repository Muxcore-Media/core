// Package spool signature verification for marketplace DeployTag trust.
//
// Env:
//
//	MUXCORE_SPOOL_REQUIRE_SIGNATURE=1  — fail deploy when signature missing/invalid
//	                                     (always on for marketplace DeployTag and
//	                                     orphan resurrection in the household profile)
//	MUXCORE_SPOOL_PUBLIC_KEY=/path     — single ed25519 public key (raw 32B, hex, base64,
//	                                     PKIX PEM, or minisign .pub format)
//	MUXCORE_SPOOL_TRUSTED_KEYS_DIR=/d  — directory of trusted public key files (any of the
//	                                     formats above); tried in lexicographic order
//	MUXCORE_SPOOL_ALLOWED_PUBLISHERS=a,b — when set, TagModule.Publisher must match
//	                                       (case-insensitive); empty publisher fails
//
// Signature sources (first match wins):
//  1. inline hex/base64 from TagModule.Signature (raw ed25519 over artifact bytes)
//  2. sidecar {artifact}.sig (raw 64B or hex/base64 text)
//  3. sidecar {artifact}.minisig (minisign detached: legacy Ed or hashed ED / Blake2b-512)
package spool

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/crypto/blake2b"
)

// RequireSignature reports whether MUXCORE_SPOOL_REQUIRE_SIGNATURE=1.
func RequireSignature() bool {
	return os.Getenv("MUXCORE_SPOOL_REQUIRE_SIGNATURE") == "1"
}

// PublicKeyPath returns MUXCORE_SPOOL_PUBLIC_KEY.
func PublicKeyPath() string {
	return strings.TrimSpace(os.Getenv("MUXCORE_SPOOL_PUBLIC_KEY"))
}

// TrustedKeysDir returns MUXCORE_SPOOL_TRUSTED_KEYS_DIR.
func TrustedKeysDir() string {
	return strings.TrimSpace(os.Getenv("MUXCORE_SPOOL_TRUSTED_KEYS_DIR"))
}

// AllowedPublishers returns the configured publisher allowlist (empty = unrestricted).
func AllowedPublishers() []string {
	raw := strings.TrimSpace(os.Getenv("MUXCORE_SPOOL_ALLOWED_PUBLISHERS"))
	if raw == "" {
		return nil
	}
	var out []string
	for _, p := range strings.Split(raw, ",") {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// CheckPublisherAllowlist enforces MUXCORE_SPOOL_ALLOWED_PUBLISHERS when set.
// Empty allowlist means no restriction. Matching is case-insensitive.
func CheckPublisherAllowlist(publisher string) error {
	allowed := AllowedPublishers()
	if len(allowed) == 0 {
		return nil
	}
	publisher = strings.TrimSpace(publisher)
	if publisher == "" {
		return fmt.Errorf("MUXCORE_SPOOL_ALLOWED_PUBLISHERS is set but module publisher is empty")
	}
	for _, a := range allowed {
		if strings.EqualFold(a, publisher) {
			return nil
		}
	}
	return fmt.Errorf("publisher %q is not in MUXCORE_SPOOL_ALLOWED_PUBLISHERS", publisher)
}

// VerifyArtifactSignature verifies an artifact against an optional inline
// signature and/or sidecar files using configured public key(s).
//
// When REQUIRE_SIGNATURE is off and no signature material is present, returns nil.
// When REQUIRE_SIGNATURE is on, a valid signature is mandatory.
func VerifyArtifactSignature(artifactPath, inlineSignature string) error {
	return VerifyArtifactSignatureRequired(artifactPath, inlineSignature, false)
}

// VerifyArtifactSignatureRequired is VerifyArtifactSignature with an extra
// requirement from the caller: when require is true (household-profile
// marketplace deploys, FR-EXT-003) a valid signature is mandatory even if
// MUXCORE_SPOOL_REQUIRE_SIGNATURE is not set. The env opt-in still applies
// when require is false.
func VerifyArtifactSignatureRequired(artifactPath, inlineSignature string, require bool) error {
	require = require || RequireSignature()
	inlineSignature = strings.TrimSpace(inlineSignature)

	kind, material, sigSrc, err := resolveSignatureMaterial(artifactPath, inlineSignature)
	if err != nil {
		return err
	}
	if kind == sigNone {
		if require {
			return fmt.Errorf("spool signature required but none found for %s (set tag signature or sidecar .sig/.minisig)", artifactPath)
		}
		return nil
	}

	pubs, err := LoadTrustedPublicKeys()
	if err != nil {
		return err
	}
	if len(pubs) == 0 {
		if require {
			return fmt.Errorf("spool signature required but no public keys configured (MUXCORE_SPOOL_PUBLIC_KEY or MUXCORE_SPOOL_TRUSTED_KEYS_DIR)")
		}
		return nil
	}

	data, err := os.ReadFile(artifactPath) //nolint:gosec // artifact path comes from validated spool layout
	if err != nil {
		return fmt.Errorf("read artifact for signature: %w", err)
	}

	var lastErr error
	for _, pub := range pubs {
		switch kind {
		case sigRaw:
			if ed25519.Verify(pub, data, material.rawSig) {
				return nil
			}
			lastErr = fmt.Errorf("signature verification failed for %s (source %s)", artifactPath, sigSrc)
		case sigMinisign:
			if err := verifyMinisign(pub, data, material.minisig); err != nil {
				lastErr = fmt.Errorf("%w (source %s)", err, sigSrc)
				continue
			}
			return nil
		}
	}
	if lastErr != nil {
		return lastErr
	}
	return fmt.Errorf("signature verification failed for %s (source %s)", artifactPath, sigSrc)
}

type sigKind int

const (
	sigNone sigKind = iota
	sigRaw
	sigMinisign
)

type sigMaterial struct { //nolint:govet // groups parsed signature bytes for verification
	rawSig  []byte
	minisig *minisignDetached
}

func resolveSignatureMaterial(artifactPath, inline string) (sigKind, sigMaterial, string, error) {
	if inline != "" {
		if looksLikeMinisign(inline) {
			ms, err := parseMinisignDetached([]byte(inline))
			if err != nil {
				return sigNone, sigMaterial{}, "inline", fmt.Errorf("decode inline minisign: %w", err)
			}
			return sigMinisign, sigMaterial{minisig: ms}, "inline", nil
		}
		sig, err := decodeSignatureBytes(inline)
		if err != nil {
			return sigNone, sigMaterial{}, "inline", fmt.Errorf("decode inline signature: %w", err)
		}
		return sigRaw, sigMaterial{rawSig: sig}, "inline", nil
	}
	for _, ext := range []string{".sig", ".minisig"} {
		path := artifactPath + ext
		raw, readErr := os.ReadFile(path) //nolint:gosec // signature sidecar path is derived from artifact path
		if readErr != nil {
			if os.IsNotExist(readErr) {
				continue
			}
			return sigNone, sigMaterial{}, ext, readErr
		}
		if ext == ".minisig" || looksLikeMinisign(string(raw)) {
			ms, err := parseMinisignDetached(raw)
			if err != nil {
				return sigNone, sigMaterial{}, path, fmt.Errorf("parse %s: %w", path, err)
			}
			return sigMinisign, sigMaterial{minisig: ms}, path, nil
		}
		sig, err := decodeSignatureFile(raw)
		if err != nil {
			return sigNone, sigMaterial{}, path, fmt.Errorf("parse %s: %w", path, err)
		}
		return sigRaw, sigMaterial{rawSig: sig}, path, nil
	}
	return sigNone, sigMaterial{}, "", nil
}

func looksLikeMinisign(s string) bool {
	s = strings.TrimSpace(s)
	return strings.Contains(s, "untrusted comment:") || strings.Contains(s, "trusted comment:")
}

func decodeSignatureFile(raw []byte) ([]byte, error) {
	if len(raw) == ed25519.SignatureSize {
		out := make([]byte, len(raw))
		copy(out, raw)
		return out, nil
	}
	return decodeSignatureBytes(string(raw))
}

func decodeSignatureBytes(s string) ([]byte, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, fmt.Errorf("empty signature")
	}
	// Strip optional "ed25519:" prefix.
	if i := strings.IndexByte(s, ':'); i >= 0 && !strings.Contains(s[:i], " ") {
		s = strings.TrimSpace(s[i+1:])
	}
	if b, err := hex.DecodeString(s); err == nil && len(b) == ed25519.SignatureSize {
		return b, nil
	}
	if b, err := base64.StdEncoding.DecodeString(s); err == nil && len(b) == ed25519.SignatureSize {
		return b, nil
	}
	if b, err := base64.RawStdEncoding.DecodeString(s); err == nil && len(b) == ed25519.SignatureSize {
		return b, nil
	}
	return nil, fmt.Errorf("signature must be 64-byte ed25519 (hex or base64)")
}

// LoadTrustedPublicKeys loads keys from MUXCORE_SPOOL_PUBLIC_KEY and/or
// MUXCORE_SPOOL_TRUSTED_KEYS_DIR. Duplicate key material is de-duplicated.
func LoadTrustedPublicKeys() ([]ed25519.PublicKey, error) {
	seen := map[string]struct{}{}
	var pubs []ed25519.PublicKey
	add := func(pub ed25519.PublicKey) {
		k := hex.EncodeToString(pub)
		if _, ok := seen[k]; ok {
			return
		}
		seen[k] = struct{}{}
		out := make(ed25519.PublicKey, ed25519.PublicKeySize)
		copy(out, pub)
		pubs = append(pubs, out)
	}

	if path := PublicKeyPath(); path != "" {
		pub, err := LoadPublicKey(path)
		if err != nil {
			return nil, fmt.Errorf("load spool public key: %w", err)
		}
		add(pub)
	}
	if dir := TrustedKeysDir(); dir != "" {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return nil, fmt.Errorf("read trusted keys dir: %w", err)
		}
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			name := e.Name()
			if strings.HasPrefix(name, ".") || strings.HasSuffix(name, ".tmp") || strings.HasSuffix(name, ".json") || strings.HasSuffix(name, ".env") {
				continue
			}
			pub, err := LoadPublicKey(filepath.Join(dir, name))
			if err != nil {
				return nil, fmt.Errorf("load trusted key %s: %w", name, err)
			}
			add(pub)
		}
	}
	return pubs, nil
}

// LoadPublicKey reads an ed25519 public key from path.
func LoadPublicKey(path string) (ed25519.PublicKey, error) {
	raw, err := os.ReadFile(path) //nolint:gosec // trusted key path comes from operator env configuration //nolint:gosec // trusted key path comes from operator env configuration
	if err != nil {
		return nil, err
	}
	return ParsePublicKey(raw)
}

// ParsePublicKey accepts raw 32B, hex, base64, PKIX PEM, or minisign .pub text.
func ParsePublicKey(raw []byte) (ed25519.PublicKey, error) {
	if len(raw) == ed25519.PublicKeySize {
		out := make(ed25519.PublicKey, ed25519.PublicKeySize)
		copy(out, raw)
		return out, nil
	}
	text := strings.TrimSpace(string(raw))
	if strings.HasPrefix(text, "-----BEGIN") {
		block, _ := pem.Decode(raw)
		if block == nil {
			return nil, fmt.Errorf("invalid PEM public key")
		}
		// PKIX SubjectPublicKeyInfo for Ed25519 ends with the 32-byte key.
		if len(block.Bytes) >= ed25519.PublicKeySize {
			key := block.Bytes[len(block.Bytes)-ed25519.PublicKeySize:]
			out := make(ed25519.PublicKey, ed25519.PublicKeySize)
			copy(out, key)
			return out, nil
		}
		return nil, fmt.Errorf("PEM public key too short")
	}
	// minisign .pub: comment line + base64 line (42 bytes decoded).
	if strings.Contains(text, "untrusted comment:") || strings.HasPrefix(text, "RW") {
		return parseMinisignPublicKey(text)
	}
	if b, err := hex.DecodeString(text); err == nil && len(b) == ed25519.PublicKeySize {
		return ed25519.PublicKey(b), nil
	}
	if b, err := base64.StdEncoding.DecodeString(text); err == nil && len(b) == ed25519.PublicKeySize {
		return ed25519.PublicKey(b), nil
	}
	if b, err := base64.RawStdEncoding.DecodeString(text); err == nil && len(b) == ed25519.PublicKeySize {
		return ed25519.PublicKey(b), nil
	}
	return nil, fmt.Errorf("unrecognized ed25519 public key format")
}

func parseMinisignPublicKey(text string) (ed25519.PublicKey, error) {
	lines := strings.Split(text, "\n")
	var b64 string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "untrusted comment:") {
			continue
		}
		b64 = line
		break
	}
	if b64 == "" {
		return nil, fmt.Errorf("minisign public key: missing key line")
	}
	decoded, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return nil, fmt.Errorf("minisign public key base64: %w", err)
	}
	// Format: 2 byte algo + 8 byte key id + 32 byte pubkey.
	if len(decoded) < 10+ed25519.PublicKeySize {
		return nil, fmt.Errorf("minisign public key too short (%d)", len(decoded))
	}
	out := make(ed25519.PublicKey, ed25519.PublicKeySize)
	copy(out, decoded[10:10+ed25519.PublicKeySize])
	return out, nil
}

// minisignDetached is a parsed minisign signature file.
// See https://jedisct1.github.io/minisign/
type minisignDetached struct { //nolint:govet // mirrors on-disk minisign signature layout
	algo            [2]byte // "Ed" legacy or "ED" hashed
	keyID           [8]byte
	sig             [ed25519.SignatureSize]byte
	trustedComment  string
	globalSignature [ed25519.SignatureSize]byte
}

func parseMinisignDetached(raw []byte) (*minisignDetached, error) {
	lines := splitNonEmptyLines(string(raw))
	if len(lines) < 4 {
		return nil, fmt.Errorf("minisig: need comment, sig, trusted comment, global sig (got %d lines)", len(lines))
	}
	// Line 0: untrusted comment (optional prefix check)
	// Line 1: base64(algo || key_id || signature)
	// Line 2: trusted comment: ...
	// Line 3: base64(global_signature)
	sigB64 := ""
	trusted := ""
	globalB64 := ""
	for i, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "untrusted comment:") {
			continue
		}
		if strings.HasPrefix(line, "trusted comment:") {
			trusted = strings.TrimPrefix(line, "trusted comment:")
			trusted = strings.TrimSpace(trusted)
			// global sig is next non-empty line
			for j := i + 1; j < len(lines); j++ {
				g := strings.TrimSpace(lines[j])
				if g == "" || strings.HasPrefix(g, "untrusted comment:") || strings.HasPrefix(g, "trusted comment:") {
					continue
				}
				globalB64 = g
				break
			}
			break
		}
		if sigB64 == "" {
			sigB64 = line
		}
	}
	if sigB64 == "" {
		return nil, fmt.Errorf("minisig: missing signature line")
	}
	if trusted == "" || globalB64 == "" {
		return nil, fmt.Errorf("minisig: missing trusted comment or global signature")
	}
	decoded, err := base64.StdEncoding.DecodeString(sigB64)
	if err != nil {
		return nil, fmt.Errorf("minisig base64: %w", err)
	}
	const hdr = 10
	if len(decoded) < hdr+ed25519.SignatureSize {
		return nil, fmt.Errorf("minisig too short (%d)", len(decoded))
	}
	global, err := base64.StdEncoding.DecodeString(globalB64)
	if err != nil {
		return nil, fmt.Errorf("minisig global base64: %w", err)
	}
	if len(global) != ed25519.SignatureSize {
		return nil, fmt.Errorf("minisig global signature size %d", len(global))
	}
	ms := &minisignDetached{trustedComment: trusted}
	copy(ms.algo[:], decoded[0:2])
	copy(ms.keyID[:], decoded[2:10])
	copy(ms.sig[:], decoded[hdr:hdr+ed25519.SignatureSize])
	copy(ms.globalSignature[:], global)
	if ms.algo != [2]byte{'E', 'd'} && ms.algo != [2]byte{'E', 'D'} {
		return nil, fmt.Errorf("minisig: unsupported algorithm %q", string(ms.algo[:]))
	}
	return ms, nil
}

func splitNonEmptyLines(s string) []string {
	var out []string
	for _, line := range strings.Split(s, "\n") {
		if strings.TrimSpace(line) != "" {
			out = append(out, line)
		}
	}
	return out
}

func verifyMinisign(pub ed25519.PublicKey, data []byte, ms *minisignDetached) error {
	if ms == nil {
		return fmt.Errorf("minisig: nil signature")
	}
	var message []byte
	switch ms.algo {
	case [2]byte{'E', 'D'}:
		// Hashed mode: Ed25519(Blake2b-512(data))
		h, err := blake2b.New512(nil)
		if err != nil {
			return fmt.Errorf("minisig blake2b: %w", err)
		}
		_, _ = h.Write(data)
		message = h.Sum(nil)
	case [2]byte{'E', 'd'}:
		message = data
	default:
		return fmt.Errorf("minisig: unsupported algorithm %q", string(ms.algo[:]))
	}
	if !ed25519.Verify(pub, message, ms.sig[:]) {
		return fmt.Errorf("minisig signature verification failed")
	}
	globalMsg := append(ms.sig[:], []byte(ms.trustedComment)...)
	if !ed25519.Verify(pub, globalMsg, ms.globalSignature[:]) {
		return fmt.Errorf("minisig global signature verification failed")
	}
	return nil
}

// parseMinisigSignature extracts the 64-byte ed25519 signature from a minisig
// blob (legacy helper used by tests). Prefer parseMinisignDetached for verify.
func parseMinisigSignature(raw []byte) ([]byte, error) {
	ms, err := parseMinisignDetached(raw)
	if err != nil {
		// Fallback for truncated fixtures that only have comment + sig line.
		lines := strings.Split(string(raw), "\n")
		var b64 string
		for _, line := range lines {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "untrusted comment:") || strings.HasPrefix(line, "trusted comment:") {
				continue
			}
			b64 = line
			break
		}
		if b64 == "" {
			return nil, err
		}
		decoded, derr := base64.StdEncoding.DecodeString(b64)
		if derr != nil {
			return nil, err
		}
		const hdr = 10
		if len(decoded) < hdr+ed25519.SignatureSize {
			return nil, err
		}
		out := make([]byte, ed25519.SignatureSize)
		copy(out, decoded[hdr:hdr+ed25519.SignatureSize])
		return out, nil
	}
	out := make([]byte, ed25519.SignatureSize)
	copy(out, ms.sig[:])
	return out, nil
}
