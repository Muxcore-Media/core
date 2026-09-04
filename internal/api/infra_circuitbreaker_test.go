package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Muxcore-Media/core/internal/circuitbreaker"
)

func setupCircuitBreakerInfraTest(t *testing.T, cb *circuitbreaker.Registry) *Server {
	t.Helper()
	srv := NewServer(":0", "", "")
	(&InfraHandlers{CircuitBreakers: cb}).RegisterRoutes(srv)
	return srv
}

func TestInfraHandlers_CircuitBreakers(t *testing.T) {
	cb := circuitbreaker.NewRegistry(circuitbreaker.Config{FailThreshold: 1})
	cb.Trip("demo-mod")

	srv := setupCircuitBreakerInfraTest(t, cb)
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/infra/modules/circuit-breakers", nil)
	rec := httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	var body struct {
		Modules map[string]circuitbreaker.Status `json:"modules"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body.Modules["demo-mod"].State != circuitbreaker.StateOpen {
		t.Fatalf("state = %q, want OPEN", body.Modules["demo-mod"].State)
	}
}

func TestInfraHandlers_CircuitBreakersByModule(t *testing.T) {
	cb := circuitbreaker.NewRegistry(circuitbreaker.Config{
		FailThreshold:     1,
		ProbeInterval:     time.Second,
		RecoveryThreshold: 1,
	})
	cb.Trip("alpha")

	srv := setupCircuitBreakerInfraTest(t, cb)
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/infra/modules/circuit-breakers?module=alpha", nil)
	rec := httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	var body struct {
		Module         string                `json:"module"`
		CircuitBreaker circuitbreaker.Status `json:"circuit_breaker"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body.Module != "alpha" {
		t.Fatalf("module = %q, want alpha", body.Module)
	}
	if body.CircuitBreaker.State != circuitbreaker.StateOpen {
		t.Fatalf("state = %q, want OPEN", body.CircuitBreaker.State)
	}
}

func TestInfraHandlers_CircuitBreakersNilRegistry(t *testing.T) {
	srv := setupCircuitBreakerInfraTest(t, nil)
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/infra/modules/circuit-breakers", nil)
	rec := httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
}
