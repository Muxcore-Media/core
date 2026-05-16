package trace

import (
	"context"
	"net/http"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	oteltrace "go.opentelemetry.io/otel/trace"
)

// HTTPMiddleware extracts tracing headers, starts a server span, and writes
// both W3C traceparent+tracestate and legacy X-Trace-Id response headers.
//
// Headers read (in order of preference):
//   - traceparent (W3C Trace Context)
//   - X-Trace-Id (legacy, single-hop convenience header)
//
// Headers written:
//   - traceparent + tracestate (W3C Trace Context)
//   - X-Trace-Id (for legacy consumers like logging and event bus)
func HTTPMiddleware(next http.Handler) http.Handler {
	tracer := otel.Tracer("muxcore")
	propagator := propagation.TraceContext{}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := propagator.Extract(r.Context(), propagation.HeaderCarrier(r.Header))

		// Fallback: if no traceparent, extract trace ID from legacy X-Trace-Id header
		// so we can inject it into a new span context.
		legacyID := r.Header.Get("X-Trace-Id")

		ctx, span := tracer.Start(ctx, "HTTP "+r.Method+" "+r.URL.Path,
			oteltrace.WithSpanKind(oteltrace.SpanKindServer),
		)
		defer span.End()

		// Write W3C Trace Context response headers
		propagator.Inject(ctx, propagation.HeaderCarrier(w.Header()))

		// Write legacy X-Trace-Id header for backward compatibility.
		// Prefer the OTEL trace ID; fall back to incoming legacy ID; generate new UUID.
		traceID := legacyTraceID(ctx, legacyID)
		w.Header().Set("X-Trace-Id", traceID)

		// Inject the trace ID into the legacy context key for event bus consumers.
		ctx = WithTraceID(ctx, traceID)

		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// legacyTraceID returns the trace ID for the X-Trace-Id header, preferring
// the OTEL span context trace ID, then the incoming legacy ID, then a new UUID.
func legacyTraceID(ctx context.Context, fallback string) string {
	spanCtx := oteltrace.SpanContextFromContext(ctx)
	if spanCtx.IsValid() {
		return spanCtx.TraceID().String()
	}
	if fallback != "" {
		return fallback
	}
	return uuid.New().String()
}
