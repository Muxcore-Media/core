package trace

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestFromContext_Empty(t *testing.T) {
	if got := FromContext(context.Background()); got != "" {
		t.Errorf("expected empty string from empty context, got %q", got)
	}
}

func TestNewContext_GeneratesID(t *testing.T) {
	ctx := NewContext(context.Background())
	id := FromContext(ctx)
	if id == "" {
		t.Fatal("expected non-empty trace ID")
	}
	// UUID v4 is 36 chars
	if len(id) != 36 {
		t.Errorf("expected 36-char UUID, got %d chars: %s", len(id), id)
	}
}

func TestNewContext_GeneratesUnique(t *testing.T) {
	ctx1 := NewContext(context.Background())
	ctx2 := NewContext(context.Background())
	if FromContext(ctx1) == FromContext(ctx2) {
		t.Error("expected unique trace IDs")
	}
}

func TestWithTraceID_RoundTrip(t *testing.T) {
	const testID = "abc-123-def-456"
	ctx := WithTraceID(context.Background(), testID)
	if got := FromContext(ctx); got != testID {
		t.Errorf("expected %q, got %q", testID, got)
	}
}

func TestWithTraceID_Overwrites(t *testing.T) {
	ctx := NewContext(context.Background())
	original := FromContext(ctx)
	ctx = WithTraceID(ctx, "overwritten")
	if FromContext(ctx) == original {
		t.Error("expected trace ID to be overwritten")
	}
	if FromContext(ctx) != "overwritten" {
		t.Errorf("expected overwritten, got %q", FromContext(ctx))
	}
}

func TestHTTPMiddleware_GeneratesWhenMissing(t *testing.T) {
	nextCalled := false
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		nextCalled = true
		id := FromContext(r.Context())
		if id == "" {
			t.Error("expected trace ID in request context")
		}
		w.WriteHeader(http.StatusOK)
	})

	mw := HTTPMiddleware(handler)
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	rec := httptest.NewRecorder()
	mw.ServeHTTP(rec, req)

	if !nextCalled {
		t.Fatal("next handler was not called")
	}
	respID := rec.Header().Get("X-Trace-Id")
	if respID == "" {
		t.Error("expected X-Trace-Id response header")
	}
}

func TestHTTPMiddleware_PreservesProvided(t *testing.T) {
	const providedID = "user-provided-trace-id"
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := FromContext(r.Context()); got != providedID {
			t.Errorf("expected %q, got %q", providedID, got)
		}
	})

	mw := HTTPMiddleware(handler)
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("X-Trace-Id", providedID)
	rec := httptest.NewRecorder()
	mw.ServeHTTP(rec, req)

	if got := rec.Header().Get("X-Trace-Id"); got != providedID {
		t.Errorf("expected X-Trace-Id header %q, got %q", providedID, got)
	}
}

func TestFromContext_WrongType(t *testing.T) {
	ctx := context.WithValue(context.Background(), ctxKey{}, 12345)
	if got := FromContext(ctx); got != "" {
		t.Errorf("expected empty string for wrong type, got %q", got)
	}
}
