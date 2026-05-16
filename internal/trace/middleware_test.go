package trace

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

func TestHTTPMiddleware_TraceparentPropagation(t *testing.T) {
	handler := HTTPMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/api/test", nil)
	req.Header.Set("traceparent", "00-0af7651916cd43dd8448eb211c80319c-b7ad6b7169203331-01")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	resp := rec.Result()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}

	// Response should include traceparent header.
	if tp := resp.Header.Get("traceparent"); tp == "" {
		t.Error("response should include traceparent header")
	}

	// Response should include X-Trace-Id for backward compat.
	if xt := resp.Header.Get("X-Trace-Id"); xt == "" {
		t.Error("response should include X-Trace-Id header")
	}
}

func TestHTTPMiddleware_LegacyHeaderFallback(t *testing.T) {
	handler := HTTPMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/api/test", nil)
	req.Header.Set("X-Trace-Id", "legacy-trace-123")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	resp := rec.Result()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}

	// Response should include X-Trace-Id even without traceparent.
	traceID := resp.Header.Get("X-Trace-Id")
	if traceID == "" {
		t.Error("response should include X-Trace-Id header when only legacy header sent")
	}
}

func TestHTTPMiddleware_GeneratesTraceID(t *testing.T) {
	handler := HTTPMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/api/test", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	resp := rec.Result()
	traceID := resp.Header.Get("X-Trace-Id")
	if traceID == "" {
		t.Error("response should generate X-Trace-Id when no headers sent")
	}
}

func TestHTTPMiddleware_500Status(t *testing.T) {
	handler := HTTPMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))

	req := httptest.NewRequest("GET", "/api/error", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	resp := rec.Result()
	if resp.StatusCode != http.StatusInternalServerError {
		t.Errorf("expected 500, got %d", resp.StatusCode)
	}

	// Headers should still be present on error responses.
	if resp.Header.Get("X-Trace-Id") == "" {
		t.Error("X-Trace-Id should be present even on error responses")
	}
}

func TestStatusRecorder_CapturesStatus(t *testing.T) {
	w := httptest.NewRecorder()
	rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}

	rec.WriteHeader(http.StatusNotFound)
	if rec.status != http.StatusNotFound {
		t.Errorf("expected 404, got %d", rec.status)
	}

	// Second WriteHeader should be ignored.
	rec.WriteHeader(http.StatusOK)
	if rec.status != http.StatusNotFound {
		t.Errorf("status should not change after first WriteHeader, got %d", rec.status)
	}
}

func TestStatusRecorder_Implicit200(t *testing.T) {
	w := httptest.NewRecorder()
	rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}

	rec.Write([]byte("hello"))
	if rec.status != http.StatusOK {
		t.Errorf("Write without WriteHeader should keep status 200, got %d", rec.status)
	}
}

// ---------------------------------------------------------------------------
// Span attribute verification (requires in-memory exporter)
// ---------------------------------------------------------------------------

// memExporter collects spans in memory for test inspection.
type memExporter struct {
	mu    sync.Mutex
	spans []sdktrace.ReadOnlySpan
}

func (e *memExporter) ExportSpans(ctx context.Context, spans []sdktrace.ReadOnlySpan) error {
	e.mu.Lock()
	e.spans = append(e.spans, spans...)
	e.mu.Unlock()
	return nil
}

func (e *memExporter) Shutdown(ctx context.Context) error { return nil }

func (e *memExporter) reset() {
	e.mu.Lock()
	e.spans = nil
	e.mu.Unlock()
}

// spanByName returns the first span with the given name, or nil.
func (e *memExporter) spanByName(name string) sdktrace.ReadOnlySpan {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, s := range e.spans {
		if s.Name() == name {
			return s
		}
	}
	return nil
}

// setupTestProvider installs a TracerProvider backed by memExporter.
// Uses SimpleSpanProcessor so spans are exported synchronously.
func setupTestProvider() (*memExporter, func()) {
	exp := &memExporter{}
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithSpanProcessor(sdktrace.NewSimpleSpanProcessor(exp)),
	)
	otel.SetTracerProvider(tp)
	return exp, func() {
		_ = tp.Shutdown(context.Background())
	}
}

func TestMiddlewareSetsHTTPAttributes(t *testing.T) {
	exp, shutdown := setupTestProvider()
	defer shutdown()

	handler := HTTPMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("POST", "/api/items", nil)
	req.Host = "muxcore.local"
	req.Header.Set("User-Agent", "TestAgent/1.0")
	req.RemoteAddr = "10.0.0.1:12345"
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	span := exp.spanByName("HTTP POST /api/items")
	if span == nil {
		t.Fatal("expected a span named 'HTTP POST /api/items'")
	}

	attrs := span.Attributes()

	mustHave := func(key, want string) {
		for _, a := range attrs {
			if string(a.Key) == key {
				got := fmt.Sprintf("%v", a.Value.AsInterface())
				if got == want {
					return
				}
				t.Fatalf("attribute %s: got %q, want %q", key, got, want)
			}
		}
		t.Fatalf("attribute %s not found in span", key)
	}

	mustHave("http.request.method", "POST")
	mustHave("url.path", "/api/items")
	mustHave("server.address", "muxcore.local")
	mustHave("user_agent.name", "TestAgent/1.0")
	mustHave("http.response.status_code", "200")
	mustHave("client.address", "10.0.0.1:12345")
}

func TestMiddlewareSpanNameFormat(t *testing.T) {
	exp, shutdown := setupTestProvider()
	defer shutdown()

	handler := HTTPMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	tests := []struct {
		method, path, expected string
	}{
		{"GET", "/health", "HTTP GET /health"},
		{"POST", "/api/v1/search", "HTTP POST /api/v1/search"},
		{"DELETE", "/admin/users/42", "HTTP DELETE /admin/users/42"},
	}

	for _, tt := range tests {
		exp.reset()
		req := httptest.NewRequest(tt.method, tt.path, nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if span := exp.spanByName(tt.expected); span == nil {
			t.Errorf("expected span %q, but no matching span found", tt.expected)
		}
	}
}

func TestMiddlewarePassthroughBody(t *testing.T) {
	handler := HTTPMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"ok":true}`))
	}))

	req := httptest.NewRequest("GET", "/api/test", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Body.String() != `{"ok":true}` {
		t.Fatalf("expected body to pass through, got %q", rec.Body.String())
	}
}

