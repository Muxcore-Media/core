package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Muxcore-Media/core/pkg/contracts"
)

func TestNewServer(t *testing.T) {
	srv := NewServer(":0")
	if srv.mux == nil {
		t.Fatal("expected mux to be initialized")
	}
	if srv.RateLimiter == nil {
		t.Fatal("expected RateLimiter to be initialized")
	}
	if srv.http == nil {
		t.Fatal("expected http.Server to be initialized")
	}
}

func TestHealthEndpoint_Simple(t *testing.T) {
	srv := NewServer(":0")
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
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
	srv := NewServer(":0")
	req := httptest.NewRequest(http.MethodPost, "/health", nil)
	rec := httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", rec.Code)
	}
}

func TestHealthEndpoint_WithHealthChecker(t *testing.T) {
	srv := NewServer(":0")
	srv.SetHealthChecker(func() map[string]error {
		return map[string]error{"module-a": nil, "module-b": nil}
	})
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
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
	srv := NewServer(":0")
	srv.SetHealthChecker(func() map[string]error {
		return map[string]error{"module-a": nil, "module-b": fmt.Errorf("connection refused")}
	})
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503, got %d", rec.Code)
	}
}

func TestHealthEndpoint_HXRequest(t *testing.T) {
	srv := NewServer(":0")
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	req.Header.Set("HX-Request", "true")
	rec := httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, req)
	if ct := rec.Header().Get("Content-Type"); ct != "text/html" {
		t.Errorf("expected text/html, got %q", ct)
	}
}

func TestAuthMiddleware_AllowsHealth(t *testing.T) {
	srv := NewServer(":0")
	srv.SetAuthFunc(func(r *http.Request) (*contracts.Session, error) {
		return nil, errors.New("should not be called")
	})
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()
	srv.http.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}
}

func TestAuthMiddleware_BlocksUnauthenticated(t *testing.T) {
	srv := NewServer(":0")
	srv.SetAuthFunc(func(r *http.Request) (*contracts.Session, error) {
		return nil, errors.New("invalid token")
	})
	srv.HandleFunc("/api/test", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
	rec := httptest.NewRecorder()
	srv.http.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rec.Code)
	}
}

func TestAuthMiddleware_AllowsAuthenticated(t *testing.T) {
	srv := NewServer(":0")
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
	req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
	rec := httptest.NewRecorder()
	srv.http.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}
}

func TestAuthMiddleware_NoAuthFunc(t *testing.T) {
	srv := NewServer(":0")
	srv.HandleFunc("/api/test", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
	rec := httptest.NewRecorder()
	srv.http.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("expected 200 in open mode, got %d", rec.Code)
	}
}

func TestRateLimiter_AllowsUnderLimit(t *testing.T) {
	srv := NewServer(":0")
	srv.HandleFunc("/api/test", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	for i := 0; i < 5; i++ {
		req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
		rec := httptest.NewRecorder()
		srv.http.Handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Errorf("request %d: expected 200, got %d", i, rec.Code)
			break
		}
	}
}

func TestRateLimiter_BlocksAfterExhaustion(t *testing.T) {
	srv := NewServer(":0")
	srv.RateLimiter = NewRateLimiter(2, time.Hour)
	srv.rebuildChain()
	srv.HandleFunc("/api/test", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	for i := 0; i < 3; i++ {
		req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
		rec := httptest.NewRecorder()
		srv.http.Handler.ServeHTTP(rec, req)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
	rec := httptest.NewRecorder()
	srv.http.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusTooManyRequests {
		t.Errorf("expected 429, got %d", rec.Code)
	}
}

func TestRateLimiter_AllowsHealthBypass(t *testing.T) {
	srv := NewServer(":0")
	srv.RateLimiter = NewRateLimiter(1, time.Hour)
	srv.rebuildChain()
	// Consume the one token
	req := httptest.NewRequest(http.MethodGet, "/anything", nil)
	rec := httptest.NewRecorder()
	srv.http.Handler.ServeHTTP(rec, req)
	// Health should still pass
	req2 := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec2 := httptest.NewRecorder()
	srv.http.Handler.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusOK {
		t.Errorf("expected 200 for /health bypass, got %d", rec2.Code)
	}
}

func TestRateLimiter_Disabled(t *testing.T) {
	srv := NewServer(":0")
	srv.RateLimiter.SetEnabled(false)
	srv.HandleFunc("/api/test", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	for i := 0; i < 200; i++ {
		req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
		rec := httptest.NewRecorder()
		srv.http.Handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Errorf("request %d: expected 200, got %d", i, rec.Code)
			break
		}
	}
}

func TestRecoveryMiddleware_CatchesPanic(t *testing.T) {
	srv := NewServer(":0")
	srv.HandleFunc("/panic", func(w http.ResponseWriter, r *http.Request) {
		panic("test panic")
	})
	req := httptest.NewRequest(http.MethodGet, "/panic", nil)
	rec := httptest.NewRecorder()
	srv.http.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("expected 500, got %d", rec.Code)
	}
}

func TestHandle_RegistersRoute(t *testing.T) {
	srv := NewServer(":0")
	srv.Handle("/custom", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	}))
	req := httptest.NewRequest(http.MethodGet, "/custom", nil)
	rec := httptest.NewRecorder()
	srv.http.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusTeapot {
		t.Errorf("expected 418, got %d", rec.Code)
	}
}

func TestHandleFunc_RegistersRoute(t *testing.T) {
	srv := NewServer(":0")
	srv.HandleFunc("/fn", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusAccepted)
	})
	req := httptest.NewRequest(http.MethodGet, "/fn", nil)
	rec := httptest.NewRecorder()
	srv.http.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Errorf("expected 202, got %d", rec.Code)
	}
}

func TestServerStartShutdown(t *testing.T) {
	srv := NewServer(":0")
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
		if err != nil && err != http.ErrServerClosed {
			t.Errorf("expected nil or ErrServerClosed, got %v", err)
		}
	case <-ctx.Done():
		t.Fatal("timeout waiting for server to stop")
	}
}

func TestGetSession_Empty(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	session, ok := GetSession(req)
	if ok {
		t.Error("expected ok=false for empty context")
	}
	if session != nil {
		t.Error("expected nil session")
	}
}

func TestGetSession_AfterAuth(t *testing.T) {
	srv := NewServer(":0")
	srv.SetAuthFunc(func(r *http.Request) (*contracts.Session, error) {
		return &contracts.Session{UserID: "auth-user"}, nil
	})
	var capturedSession *contracts.Session
	srv.HandleFunc("/api/whoami", func(w http.ResponseWriter, r *http.Request) {
		s, _ := GetSession(r)
		capturedSession = s
		w.WriteHeader(http.StatusOK)
	})
	req := httptest.NewRequest(http.MethodGet, "/api/whoami", nil)
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
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			for k, v := range tt.headers {
				req.Header.Set(k, v)
			}
			req.RemoteAddr = tt.remote
			ip := extractClientIP(req)
			if ip != tt.expected {
				t.Errorf("expected %q, got %q", tt.expected, ip)
			}
		})
	}
}
