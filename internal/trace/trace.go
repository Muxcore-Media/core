package trace

import (
	"context"

	"github.com/google/uuid"
	oteltrace "go.opentelemetry.io/otel/trace"
)

type ctxKey struct{}

// FromContext extracts the trace ID from context, preferring OTEL span context
// and falling back to the legacy X-Trace-Id key.
func FromContext(ctx context.Context) string {
	spanCtx := oteltrace.SpanContextFromContext(ctx)
	if spanCtx.IsValid() {
		return spanCtx.TraceID().String()
	}
	if v, ok := ctx.Value(ctxKey{}).(string); ok {
		return v
	}
	return ""
}

// WithTraceID returns a context with the given trace ID stored in the legacy key.
// Event bus consumers use this to propagate trace IDs to handler goroutines.
func WithTraceID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, ctxKey{}, id)
}

// NewContext returns a context with a new UUID-based trace ID.
func NewContext(ctx context.Context) context.Context {
	return context.WithValue(ctx, ctxKey{}, uuid.New().String())
}
