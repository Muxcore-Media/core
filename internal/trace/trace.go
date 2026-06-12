package trace

import (
	"context"
	"net/http"
	"strings"

	"github.com/google/uuid"
)

type ctxKey struct{}

// FromContext extracts the trace ID from context, or returns "".
func FromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	if v, ok := ctx.Value(ctxKey{}).(string); ok {
		return v
	}
	return ""
}

// NewContext returns a context with a new trace ID.
func NewContext(ctx context.Context) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, ctxKey{}, uuid.New().String())
}

// WithTraceID returns a context with the given trace ID.
func WithTraceID(ctx context.Context, id string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, ctxKey{}, id)
}

// HTTPMiddleware extracts or generates a trace ID from the X-Trace-Id header
// and injects it into the request context.
func HTTPMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		traceID := r.Header.Get("X-Trace-Id")
		if traceID == "" || len(traceID) > 64 || !isValidTraceID(traceID) {
			traceID = uuid.New().String()
		}
		w.Header().Set("X-Trace-Id", traceID)
		ctx := WithTraceID(r.Context(), traceID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// isValidTraceID checks if the string looks like a valid trace identifier.
// Accepts UUIDs and hex strings up to 64 characters.
// Rejects strings containing CR/LF to prevent HTTP header injection (CWE-93).
func isValidTraceID(s string) bool {
	// Block CRLF injection vectors.
	if strings.ContainsAny(s, "\r\n") {
		return false
	}
	// Try parsing as UUID first
	if _, err := uuid.Parse(s); err == nil {
		return true
	}
	// Also accept hex strings (common in distributed tracing)
	for _, c := range s {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') && (c < 'A' || c > 'F') && c != '-' {
			return false
		}
	}
	return len(s) > 0
}
