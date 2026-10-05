// Package enroll implements mesh module enrollment (ADR-0017 decision 3):
//
//   - Deterministic enrollment tokens: mct_2_<id>_ followed by
//     hex(HMAC-SHA256(MUXCORE_ENROLL_SECRET, id)).
//   - A persisted single-use ledger (<ca dir>/enrolled.json) recording which
//     module IDs have enrolled, so each token works once until an operator
//     runs `muxcored enroll reset <id>`.
//   - CSR parsing and the SAN policy (module ID + loopback + an operator
//     allow-list, MUXCORE_ENROLL_SAN_ALLOW).
//
// It is a leaf package shared by the core CA (internal/grpcmesh), the
// registration service (internal/module/mgr) and the muxcored CLI.
package enroll

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// Environment variables read by muxcored.
const (
	// EnvSecret is the HMAC key for version-2 enrollment tokens. When unset,
	// only core's in-memory (version 1, 5-minute) tokens are accepted.
	EnvSecret = "MUXCORE_ENROLL_SECRET" //nolint:gosec // env var name, not a credential
	// EnvSANAllow is a comma-separated allow-list of extra DNS names (or IP
	// addresses) a module may request as SANs. Entries are exact names or
	// "*.suffix" wildcards. Default: none (module ID and loopback only).
	EnvSANAllow = "MUXCORE_ENROLL_SAN_ALLOW"
)

// TokenPrefix starts every version-2 enrollment token.
const TokenPrefix = "mct_2_"

// MinSecretLen is the minimum MUXCORE_ENROLL_SECRET length in bytes.
const MinSecretLen = 16

// macHexLen is the length of the hex-encoded HMAC-SHA256 suffix.
const macHexLen = sha256.Size * 2

var (
	// ErrNoSecret is returned when a version-2 token is presented but no
	// enrollment secret is configured.
	ErrNoSecret = errors.New("enrollment secret not configured (" + EnvSecret + ")")
	// ErrBadToken is returned for malformed or forged tokens.
	ErrBadToken = errors.New("invalid enrollment token")
	// ErrAlreadyEnrolled is returned when the token's module ID is already in
	// the ledger (the token was used).
	ErrAlreadyEnrolled = errors.New("enrollment token already used — run `muxcored enroll reset <id>` to re-enroll")
)

// moduleIDRe limits module IDs to characters that are safe as a certificate
// Common Name, a DNS SAN label set and a token segment. 64 is the X.509
// upper bound for a Common Name.
var moduleIDRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

// ValidModuleID reports whether id can be enrolled.
func ValidModuleID(id string) error {
	if !moduleIDRe.MatchString(id) {
		return fmt.Errorf("invalid module ID %q: use 1-64 letters, digits, '.', '_' or '-'", id)
	}
	return nil
}

// ValidSecret checks MUXCORE_ENROLL_SECRET.
func ValidSecret(secret string) error {
	if len(secret) < MinSecretLen {
		return fmt.Errorf("%s must be at least %d bytes", EnvSecret, MinSecretLen)
	}
	return nil
}

// Token returns the version-2 enrollment token for id.
func Token(secret []byte, id string) string {
	return TokenPrefix + id + "_" + hex.EncodeToString(mac(secret, id))
}

func mac(secret []byte, id string) []byte {
	h := hmac.New(sha256.New, secret)
	_, _ = h.Write([]byte(id))
	return h.Sum(nil)
}

// IsV2 reports whether token is a version-2 enrollment token.
func IsV2(token string) bool { return strings.HasPrefix(token, TokenPrefix) }

// TokenModuleID returns the module ID embedded in a version-2 token without
// verifying it. Module IDs may contain '_', so the MAC is the last segment.
func TokenModuleID(token string) (string, bool) {
	if !IsV2(token) {
		return "", false
	}
	rest := strings.TrimPrefix(token, TokenPrefix)
	i := strings.LastIndexByte(rest, '_')
	if i <= 0 || len(rest)-i-1 != macHexLen {
		return "", false
	}
	return rest[:i], true
}

// VerifyToken checks a version-2 token's MAC and returns its module ID. It
// does not consult the ledger.
func VerifyToken(secret []byte, token string) (string, error) {
	if len(secret) == 0 {
		return "", ErrNoSecret
	}
	id, ok := TokenModuleID(token)
	if !ok {
		return "", ErrBadToken
	}
	if ValidModuleID(id) != nil {
		return "", ErrBadToken
	}
	got, err := hex.DecodeString(token[len(token)-macHexLen:])
	if err != nil || !hmac.Equal(got, mac(secret, id)) {
		return "", ErrBadToken
	}
	return id, nil
}

// Verifier validates version-2 tokens against the secret and consumes them
// in the ledger.
type Verifier struct {
	ledger *Ledger
	secret []byte
}

// NewVerifier returns a Verifier. secret must pass ValidSecret.
func NewVerifier(secret string, ledger *Ledger) (*Verifier, error) {
	if err := ValidSecret(secret); err != nil {
		return nil, err
	}
	if ledger == nil {
		return nil, errors.New("enroll: ledger is required")
	}
	return &Verifier{secret: []byte(secret), ledger: ledger}, nil
}

// Validate verifies token and records its module ID in the ledger. A
// second Validate for the same ID fails with ErrAlreadyEnrolled until the
// ledger entry is reset.
func (v *Verifier) Validate(token string) (string, error) {
	id, err := VerifyToken(v.secret, token)
	if err != nil {
		return "", err
	}
	if err := v.ledger.Consume(id); err != nil {
		return "", err
	}
	return id, nil
}
