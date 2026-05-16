package trace

import (
	"net/http"
	"net/http/httptest"
	"testing"
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
