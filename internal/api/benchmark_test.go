package api

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/Muxcore-Media/core/pkg/contracts"
)

func BenchmarkAPIServer_HealthEndpoint(b *testing.B) {
	os.Setenv("MUXCORE_DEV_TLS_SKIP", "true")
	srv := NewServer(":0", "", "")
	defer srv.Shutdown(context.Background())

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		srv.http.Handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			b.Fatalf("expected 200, got %d", rec.Code)
		}
	}
}

func BenchmarkAPIServer_AuthSuccess(b *testing.B) {
	for _, n := range []int{1, 10, 100} {
		b.Run(fmt.Sprintf("routes=%d", n), func(b *testing.B) {
			os.Setenv("MUXCORE_DEV_TLS_SKIP", "true")
			srv := NewServer(":0", "", "")
			defer srv.Shutdown(context.Background())

			srv.SetAuthFunc(func(r *http.Request) (*contracts.Session, error) {
				return &contracts.Session{UserID: "bench", Username: "bench"}, nil
			})
			srv.SetAuthorizer(&allowAllAuthorizer{})

			for i := 0; i < n; i++ {
				path := fmt.Sprintf("/api/resource/%d", i)
				srv.RouteRequire(path, "read", "resource")
				srv.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
					w.WriteHeader(http.StatusOK)
				})
			}

			req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/resource/0", nil)
			rec := httptest.NewRecorder()

			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				srv.http.Handler.ServeHTTP(rec, req)
				if rec.Code != http.StatusOK {
					b.Fatalf("expected 200, got %d", rec.Code)
				}
			}
		})
	}
}

func BenchmarkAPIServer_AuthFailure(b *testing.B) {
	os.Setenv("MUXCORE_DEV_TLS_SKIP", "true")
	srv := NewServer(":0", "", "")
	defer srv.Shutdown(context.Background())

	srv.SetAuthFunc(func(r *http.Request) (*contracts.Session, error) {
		return nil, fmt.Errorf("bad credentials")
	})
	srv.HandleFunc("/api/secret", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/secret", nil)
	req.RemoteAddr = "10.0.0.1:12345"
	rec := httptest.NewRecorder()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		srv.http.Handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			b.Fatalf("expected 401, got %d", rec.Code)
		}
	}
}

// allowAllAuthorizer permits every action.
type allowAllAuthorizer struct{}

func (allowAllAuthorizer) Can(_ context.Context, _ contracts.Session, _, _ string) (bool, error) {
	return true, nil
}
