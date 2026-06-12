package main

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Muxcore-Media/core/internal/bootstrap"
	"github.com/Muxcore-Media/core/internal/config"
)

func TestExtractBearerToken(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		header string
		want   string
	}{
		{"valid bearer", "Bearer abc123", "abc123"},
		{"missing header", "", ""},
		{"basic scheme", "Basic dXNlcjpwYXNz", ""},
		{"empty bearer value", "Bearer ", ""},
		{"bearer only", "Bearer", ""},
		{"case insensitive BEARER", "BEARER token123", "token123"},
		{"case insensitive bearer", "bearer token123", "token123"},
		{"case insensitive BeArEr", "BeArEr token123", "token123"},
		{"token with special chars", "Bearer eyJhbGciOiJSUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0", "eyJhbGciOiJSUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0"},
		{"token with spaces in value", "Bearer token with spaces", "token with spaces"},
		{"shorter than prefix", "Bear", ""},
		{"digest scheme", "Digest username=test", ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := httptest.NewRequest(http.MethodGet, "/", nil)
			if tc.header != "" {
				r.Header.Set("Authorization", tc.header)
			}
			got := bootstrap.ExtractBearerToken(r)
			if got != tc.want {
				t.Errorf("ExtractBearerToken() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestIsLocalhostAddr(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		addr string
		want bool
	}{
		{"localhost with port", "localhost:8080", true},
		{"ipv4 loopback", "127.0.0.1:9090", true},
		{"ipv6 loopback", "[::1]:8080", true},
		{"unspecified", "0.0.0.0:8080", false},
		{"private ip", "192.168.1.1:8080", false},
		{"empty string", "", true},
		{"localhost no port", "localhost", true},
		{"public ip", "8.8.8.8:443", false},
		{"ipv6 non-loopback", "[::2]:8080", false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := bootstrap.IsLocalhostAddr(tc.addr)
			if got != tc.want {
				t.Errorf("IsLocalhostAddr(%q) = %v, want %v", tc.addr, got, tc.want)
			}
		})
	}
}

func TestSetupLogger(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	tests := []struct {
		name      string
		lc        config.LogConfig
		enabLevel slog.Level
		wantEnab  bool
	}{
		{"default info", config.LogConfig{}, slog.LevelDebug, false},
		{"info allows info", config.LogConfig{}, slog.LevelInfo, true},
		{"debug level", config.LogConfig{Level: "debug"}, slog.LevelDebug, true},
		{"warn level blocks info", config.LogConfig{Level: "warn"}, slog.LevelInfo, false},
		{"warn allows warn", config.LogConfig{Level: "warn"}, slog.LevelWarn, true},
		{"error level blocks warn", config.LogConfig{Level: "error"}, slog.LevelWarn, false},
		{"error allows error", config.LogConfig{Level: "error"}, slog.LevelError, true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			logger := bootstrap.SetupLogger(tc.lc)
			if logger == nil {
				t.Fatal("SetupLogger returned nil")
			}
			if got := logger.Enabled(ctx, tc.enabLevel); got != tc.wantEnab {
				t.Errorf("Enabled(%v) = %v, want %v", tc.enabLevel, got, tc.wantEnab)
			}
		})
	}
}

func TestSetupLoggerJSONFormat(t *testing.T) {
	t.Parallel()
	logger := bootstrap.SetupLogger(config.LogConfig{Format: "json"})
	if logger == nil {
		t.Fatal("SetupLogger returned nil for json format")
	}
}

func TestDevTLSSkipCheck(t *testing.T) {
	t.Setenv("MUXCORE_INSECURE_DISABLE_TLS", "")
	t.Setenv("MUXCORE_DEV_TLS_SKIP", "")

	if bootstrap.DevTLSSkipCheck() {
		t.Error("expected false when no env vars set")
	}

	t.Setenv("MUXCORE_INSECURE_DISABLE_TLS", "true")
	if !bootstrap.DevTLSSkipCheck() {
		t.Error("expected true when MUXCORE_INSECURE_DISABLE_TLS=true")
	}

	t.Setenv("MUXCORE_INSECURE_DISABLE_TLS", "")
	t.Setenv("MUXCORE_DEV_TLS_SKIP", "true")
	if !bootstrap.DevTLSSkipCheck() {
		t.Error("expected true when MUXCORE_DEV_TLS_SKIP=true")
	}

	t.Setenv("MUXCORE_INSECURE_DISABLE_TLS", "1")
	t.Setenv("MUXCORE_DEV_TLS_SKIP", "")
	if !bootstrap.DevTLSSkipCheck() {
		t.Error("expected true when MUXCORE_INSECURE_DISABLE_TLS=1")
	}

	t.Setenv("MUXCORE_INSECURE_DISABLE_TLS", "")
	t.Setenv("MUXCORE_DEV_TLS_SKIP", "1")
	if !bootstrap.DevTLSSkipCheck() {
		t.Error("expected true when MUXCORE_DEV_TLS_SKIP=1")
	}

	t.Setenv("MUXCORE_INSECURE_DISABLE_TLS", "false")
	t.Setenv("MUXCORE_DEV_TLS_SKIP", "false")
	if bootstrap.DevTLSSkipCheck() {
		t.Error("expected false when both vars are 'false'")
	}
}
