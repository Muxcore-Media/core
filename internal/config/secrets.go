package config

import (
	"bytes"
	"fmt"
	"log/slog"
	"os"
	"strings"
)

// ReadSecretFile reads a secret from path, trimming surrounding whitespace.
// If the file is accessible to "other" users (any of the 0o007 mode bits) a
// warning is logged but the read still succeeds. The secret value is never
// logged.
func ReadSecretFile(path string) (string, error) {
	data, err := os.ReadFile(path) //nolint:gosec // path from operator env var / config
	if err != nil {
		return "", fmt.Errorf("read secret file %q: %w", path, err)
	}
	fi, statErr := os.Stat(path) //nolint:gosec // path from operator env var / config
	if statErr == nil && fi.Mode().Perm()&0o007 != 0 {
		slog.Warn("secret file is world-accessible; restrict it with chmod 600",
			"path", path, "mode", fmt.Sprintf("%#o", fi.Mode().Perm()))
	}
	return strings.TrimSpace(string(data)), nil
}

// SecretFromEnv resolves a secret from the environment variable name, or from
// the file named by name+"_FILE" (the file wins when both are set). It returns
// an empty string when neither is set. A configured but unreadable or empty
// file is an error (fail closed), so a missing key file never silently
// downgrades a security feature.
func SecretFromEnv(name string) (string, error) {
	if p := os.Getenv(name + "_FILE"); p != "" {
		v, err := ReadSecretFile(p)
		if err != nil {
			return "", fmt.Errorf("%s_FILE: %w", name, err)
		}
		if v == "" {
			return "", fmt.Errorf("%s_FILE: %q is empty", name, p)
		}
		return v, nil
	}
	return os.Getenv(name), nil
}

// minAuditHMACKeyBytes is the length below which a warning is logged.
const minAuditHMACKeyBytes = 32

// ResolveAuditHMACKey returns the audit HMAC signing key, or nil when none is
// configured (chain-only mode). Sources, in precedence order:
// MUXCORE_AUDIT_HMAC_KEY_FILE, MUXCORE_AUDIT_HMAC_KEY, then cfg.Audit.HMACKeyFile.
// Errors never contain the key.
func ResolveAuditHMACKey(cfg *Config) ([]byte, error) {
	var key string
	switch {
	case os.Getenv("MUXCORE_AUDIT_HMAC_KEY_FILE") != "" || os.Getenv("MUXCORE_AUDIT_HMAC_KEY") != "":
		v, err := SecretFromEnv("MUXCORE_AUDIT_HMAC_KEY")
		if err != nil {
			return nil, err
		}
		key = v
	case cfg != nil && cfg.Audit.HMACKeyFile != "":
		v, err := ReadSecretFile(cfg.Audit.HMACKeyFile)
		if err != nil {
			return nil, fmt.Errorf("audit.hmac_key_file: %w", err)
		}
		if v == "" {
			return nil, fmt.Errorf("audit.hmac_key_file: %q is empty", cfg.Audit.HMACKeyFile)
		}
		key = v
	}
	if key == "" {
		return nil, nil
	}
	b := []byte(key)
	if len(b) < minAuditHMACKeyBytes {
		slog.Warn("audit HMAC key is shorter than recommended", "min_bytes", minAuditHMACKeyBytes)
	}
	return bytes.Clone(b), nil
}
