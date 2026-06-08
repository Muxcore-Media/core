package api

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/Muxcore-Media/core/pkg/contracts"
)

func TestNewServer(t *testing.T) {
	os.Setenv("MUXCORE_INSECURE_DISABLE_TLS", "true")
	srv := NewServer(":0", "", "")
	if srv.mux == nil {
		t.Fatal("expected mux to be initialized")
	}
	if srv.http == nil {
		t.Fatal("expected http.Server to be initialized")
	}
}

func TestHealthEndpoint_Simple(t *testing.T) {
	os.Setenv("MUXCORE_INSECURE_DISABLE_TLS", "true")
	srv := NewServer(":0", "", "")
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}
	if rec.Body.String() == "" {
		t.Error("expected non-empty body")
	}
}

func TestHealthEndpoint_MethodNotAllowed(t *testing.T) {
	os.Setenv("MUXCORE_INSECURE_DISABLE_TLS", "true")
	srv := NewServer(":0", "", "")
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/health", nil)
	rec := httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", rec.Code)
	}
}

func TestHealthEndpoint_WithHealthChecker(t *testing.T) {
	os.Setenv("MUXCORE_INSECURE_DISABLE_TLS", "true")
	srv := NewServer(":0", "", "")
	srv.SetHealthChecker(func() map[string]error {
		return map[string]error{"module-a": nil, "module-b": nil}
	})
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("expected application/json, got %q", ct)
	}
}

func TestHealthEndpoint_Degraded(t *testing.T) {
	os.Setenv("MUXCORE_INSECURE_DISABLE_TLS", "true")
	srv := NewServer(":0", "", "")
	srv.SetHealthChecker(func() map[string]error {
		return map[string]error{"module-a": nil, "module-b": fmt.Errorf("connection refused")}
	})
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503, got %d", rec.Code)
	}
}

func TestHealthEndpoint_HXRequest(t *testing.T) {
	os.Setenv("MUXCORE_INSECURE_DISABLE_TLS", "true")
	srv := NewServer(":0", "", "")
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/health", nil)
	req.Header.Set("HX-Request", "true")
	rec := httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, req)
	if ct := rec.Header().Get("Content-Type"); ct != "text/html" {
		t.Errorf("expected text/html, got %q", ct)
	}
}

func TestAuthMiddleware_AllowsHealth(t *testing.T) {
	os.Setenv("MUXCORE_INSECURE_DISABLE_TLS", "true")
	srv := NewServer(":0", "", "")
	srv.SetAuthFunc(func(r *http.Request) (*contracts.Session, error) {
		return nil, errors.New("should not be called")
	})
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()
	srv.http.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}
}

func TestAuthMiddleware_BlocksUnauthenticated(t *testing.T) {
	os.Setenv("MUXCORE_INSECURE_DISABLE_TLS", "true")
	srv := NewServer(":0", "", "")
	srv.SetAuthFunc(func(r *http.Request) (*contracts.Session, error) {
		return nil, errors.New("invalid token")
	})
	srv.HandleFunc("/api/test", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/test", nil)
	rec := httptest.NewRecorder()
	srv.http.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rec.Code)
	}
}

func TestAuthMiddleware_AllowsAuthenticated(t *testing.T) {
	os.Setenv("MUXCORE_INSECURE_DISABLE_TLS", "true")
	srv := NewServer(":0", "", "")
	expectedSession := &contracts.Session{UserID: "user-1", Username: "test"}
	srv.SetAuthFunc(func(r *http.Request) (*contracts.Session, error) {
		return expectedSession, nil
	})
	srv.HandleFunc("/api/test", func(w http.ResponseWriter, r *http.Request) {
		session, ok := GetSession(r)
		if !ok || session.UserID != "user-1" {
			http.Error(w, "wrong user", http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/test", nil)
	rec := httptest.NewRecorder()
	srv.http.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}
}

func TestAuthMiddleware_NoAuthFunc(t *testing.T) {
	os.Setenv("MUXCORE_INSECURE_DISABLE_TLS", "true")
	srv := NewServer(":0", "", "")
	srv.HandleFunc("/api/test", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/test", nil)
	rec := httptest.NewRecorder()
	srv.http.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("expected 200 in open mode, got %d", rec.Code)
	}
}

func TestRateLimiter_AllowsUnderLimit(t *testing.T) {
	os.Setenv("MUXCORE_INSECURE_DISABLE_TLS", "true")
	srv := NewServer(":0", "", "")
	srv.HandleFunc("/api/test", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	for i := 0; i < 5; i++ {
		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/test", nil)
		rec := httptest.NewRecorder()
		srv.http.Handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Errorf("request %d: expected 200, got %d", i, rec.Code)
			break
		}
	}
}

func TestRateLimiter_Integration(t *testing.T) {
	os.Setenv("MUXCORE_INSECURE_DISABLE_TLS", "true")
	srv := NewServer(":0", "", "")
	srv.HandleFunc("/api/test", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	// With no rate limiter set, all requests should pass through.
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/test", nil)
	rec := httptest.NewRecorder()
	srv.http.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("expected 200 without rate limiter, got %d", rec.Code)
	}

	// Rate limiter integration is tested via module integration tests
	// when a RateLimiterProvider module is registered.
}

func TestRecoveryMiddleware_CatchesPanic(t *testing.T) {
	os.Setenv("MUXCORE_INSECURE_DISABLE_TLS", "true")
	srv := NewServer(":0", "", "")
	srv.HandleFunc("/panic", func(w http.ResponseWriter, r *http.Request) {
		panic("test panic")
	})
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/panic", nil)
	rec := httptest.NewRecorder()
	srv.http.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("expected 500, got %d", rec.Code)
	}
}

func TestHandle_RegistersRoute(t *testing.T) {
	os.Setenv("MUXCORE_INSECURE_DISABLE_TLS", "true")
	srv := NewServer(":0", "", "")
	srv.Handle("/custom", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	}))
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/custom", nil)
	rec := httptest.NewRecorder()
	srv.http.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusTeapot {
		t.Errorf("expected 418, got %d", rec.Code)
	}
}

func TestHandleFunc_RegistersRoute(t *testing.T) {
	os.Setenv("MUXCORE_INSECURE_DISABLE_TLS", "true")
	srv := NewServer(":0", "", "")
	srv.HandleFunc("/fn", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusAccepted)
	})
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/fn", nil)
	rec := httptest.NewRecorder()
	srv.http.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Errorf("expected 202, got %d", rec.Code)
	}
}

func TestServerStartShutdown(t *testing.T) {
	os.Setenv("MUXCORE_INSECURE_DISABLE_TLS", "true")
	srv := NewServer(":0", "", "")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	errCh := make(chan error, 1)
	go func() { errCh <- srv.Start() }()
	time.Sleep(100 * time.Millisecond)
	if err := srv.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	select {
	case err := <-errCh:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			t.Errorf("expected nil or ErrServerClosed, got %v", err)
		}
	case <-ctx.Done():
		t.Fatal("timeout waiting for server to stop")
	}
}

func TestGetSession_Empty(t *testing.T) {
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", nil)
	session, ok := GetSession(req)
	if ok {
		t.Error("expected ok=false for empty context")
	}
	if session != nil {
		t.Error("expected nil session")
	}
}

func TestGetSession_AfterAuth(t *testing.T) {
	os.Setenv("MUXCORE_INSECURE_DISABLE_TLS", "true")
	srv := NewServer(":0", "", "")
	srv.SetAuthFunc(func(r *http.Request) (*contracts.Session, error) {
		return &contracts.Session{UserID: "auth-user"}, nil
	})
	var capturedSession *contracts.Session
	srv.HandleFunc("/api/whoami", func(w http.ResponseWriter, r *http.Request) {
		s, _ := GetSession(r)
		capturedSession = s
		w.WriteHeader(http.StatusOK)
	})
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/whoami", nil)
	rec := httptest.NewRecorder()
	srv.http.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if capturedSession == nil {
		t.Fatal("expected session in context")
	}
	if capturedSession.UserID != "auth-user" {
		t.Errorf("expected auth-user, got %q", capturedSession.UserID)
	}
}

func TestSecurityHeaders_AllPresent(t *testing.T) {
	os.Setenv("MUXCORE_INSECURE_DISABLE_TLS", "true")
	srv := NewServer(":0", "", "")
	srv.HandleFunc("/check", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/check", nil)
	rec := httptest.NewRecorder()
	srv.http.Handler.ServeHTTP(rec, req)

	expected := map[string]string{
		"X-Content-Type-Options": "nosniff",
		"X-Frame-Options":        "DENY",
		"X-XSS-Protection":       "0",
		"Referrer-Policy":        "strict-origin-when-cross-origin",
	}
	for header, want := range expected {
		got := rec.Header().Get(header)
		if got != want {
			t.Errorf("header %s: got %q, want %q", header, got, want)
		}
	}
}

func TestSecurityHeaders_HSTSAbsentWithoutTLS(t *testing.T) {
	os.Setenv("MUXCORE_INSECURE_DISABLE_TLS", "true")
	srv := NewServer(":0", "", "")
	srv.HandleFunc("/h", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/h", nil)
	rec := httptest.NewRecorder()
	srv.http.Handler.ServeHTTP(rec, req)

	if hsts := rec.Header().Get("Strict-Transport-Security"); hsts != "" {
		t.Errorf("HSTS should not be set without TLS, got %q", hsts)
	}
}

func TestMaxBodyMiddleware_RejectsOversizedBody(t *testing.T) {
	os.Setenv("MUXCORE_INSECURE_DISABLE_TLS", "true")
	srv := NewServer(":0", "", "")
	srv.HandleFunc("/upload", func(w http.ResponseWriter, r *http.Request) {
		// Reading the body triggers MaxBytesReader rejection.
		if _, err := io.ReadAll(r.Body); err != nil {
			http.Error(w, "too large", http.StatusRequestEntityTooLarge)
			return
		}
		w.WriteHeader(http.StatusOK)
	})

	// Build a body larger than 10MB.
	bigBody := make([]byte, maxBodySize+1)
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/upload", bytes.NewReader(bigBody))
	rec := httptest.NewRecorder()
	srv.http.Handler.ServeHTTP(rec, req)

	if rec.Code == http.StatusOK {
		t.Error("expected oversized body to be rejected")
	}
}

func TestAuthMiddleware_BruteForceBackoff(t *testing.T) {
	os.Setenv("MUXCORE_INSECURE_DISABLE_TLS", "true")
	srv := NewServer(":0", "", "")
	srv.SetAuthFunc(func(r *http.Request) (*contracts.Session, error) {
		return nil, errors.New("bad credentials")
	})
	srv.HandleFunc("/api/secret", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	// 5 failures from the same IP should trigger the backoff on the 6th.
	for i := 0; i < 5; i++ {
		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/secret", nil)
		req.RemoteAddr = "9.9.9.9:1234"
		rec := httptest.NewRecorder()
		srv.http.Handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("request %d: expected 401, got %d", i, rec.Code)
		}
	}

	// 6th request from same IP — should be rate-limited (429).
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/secret", nil)
	req.RemoteAddr = "9.9.9.9:9999"
	rec := httptest.NewRecorder()
	srv.http.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusTooManyRequests {
		t.Errorf("6th failed auth should be rate-limited (429), got %d", rec.Code)
	}
}

func TestAuthzMiddleware_ABACPath(t *testing.T) {
	os.Setenv("MUXCORE_INSECURE_DISABLE_TLS", "true")
	srv := NewServer(":0", "", "")

	// Auth always returns a session.
	srv.SetAuthFunc(func(r *http.Request) (*contracts.Session, error) {
		return &contracts.Session{UserID: "u1", Roles: []string{"viewer"}}, nil
	})
	// ABAC authorizer — rejects everyone.
	srv.SetAuthorizer(&rejectAllAuthorizer{})
	srv.RouteRequire("/admin/stuff", "admin.access", "admin")
	srv.HandleFunc("/admin/stuff", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/admin/stuff", nil)
	rec := httptest.NewRecorder()
	srv.http.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("expected 403 from ABAC authorizer, got %d", rec.Code)
	}
}

// rejectAllAuthorizer denies everything.
type rejectAllAuthorizer struct{}

func (r *rejectAllAuthorizer) Can(_ context.Context, _ contracts.Session, _, _ string) (bool, error) {
	return false, nil
}

func TestExtractClientIP(t *testing.T) {
	tests := []struct {
		name     string
		headers  map[string]string
		remote   string
		expected string
	}{
		{"X-Forwarded-For", map[string]string{"X-Forwarded-For": "10.0.0.1"}, "127.0.0.1:1234", "10.0.0.1"},
		{"X-Real-IP", map[string]string{"X-Real-IP": "10.0.0.2"}, "127.0.0.1:1234", "10.0.0.2"},
		{"RemoteAddr", nil, "192.168.1.1:56789", "192.168.1.1"},
		{"RemoteAddr_no_port", nil, "192.168.1.1", "192.168.1.1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", nil)
			for k, v := range tt.headers {
				req.Header.Set(k, v)
			}
			defaultTrusted := []net.IPNet{
				{IP: net.IPv4(127, 0, 0, 0), Mask: net.CIDRMask(8, 32)},
				{IP: net.ParseIP("::1"), Mask: net.CIDRMask(128, 128)},
			}
			req.RemoteAddr = tt.remote
			ip := extractClientIP(req, defaultTrusted)
			if ip != tt.expected {
				t.Errorf("expected %q, got %q", tt.expected, ip)
			}
		})
	}
}
