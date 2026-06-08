package trace

import (
	"context"
	"net/http"

	"github.com/google/uuid"
)

type ctxKey struct{}

// FromContext extracts the trace ID from context, or returns "".
func FromContext(ctx context.Context) string {
	if v, ok := ctx.Value(ctxKey{}).(string); ok {
		return v
	}
	return ""
}

// NewContext returns a context with a new trace ID.
func NewContext(ctx context.Context) context.Context {
	return context.WithValue(ctx, ctxKey{}, uuid.New().String())
}

// WithTraceID returns a context with the given trace ID.
func WithTraceID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, ctxKey{}, id)
}

// HTTPMiddleware extracts or generates a trace ID from the X-Trace-Id header
// and injects it into the request context.
func HTTPMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		traceID := r.Header.Get("X-Trace-Id")
		if traceID == "" {
			traceID = uuid.New().String()
		}
		w.Header().Set("X-Trace-Id", traceID)
		ctx := WithTraceID(r.Context(), traceID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
