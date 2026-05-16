package trace

import (
	"context"
	"net/http"
	"strconv"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	oteltrace "go.opentelemetry.io/otel/trace"
)

// statusRecorder wraps http.ResponseWriter to capture the status code.
type statusRecorder struct {
	http.ResponseWriter
	status int
	wrote  bool
}

func (r *statusRecorder) WriteHeader(code int) {
	if !r.wrote {
		r.status = code
		r.wrote = true
	}
	r.ResponseWriter.WriteHeader(code)
}

func (r *statusRecorder) Write(b []byte) (int, error) {
	if !r.wrote {
		r.status = http.StatusOK
		r.wrote = true
	}
	return r.ResponseWriter.Write(b)
}

// HTTPMiddleware extracts tracing headers, starts a server span, sets HTTP
// semantic convention attributes, and writes both W3C traceparent+tracestate
// and legacy X-Trace-Id response headers.
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

		legacyID := r.Header.Get("X-Trace-Id")

		ctx, span := tracer.Start(ctx, "HTTP "+r.Method+" "+r.URL.Path,
			oteltrace.WithSpanKind(oteltrace.SpanKindServer),
		)
		defer span.End()

		// Set HTTP semantic convention attributes on the span.
		span.SetAttributes(
			semconv.HTTPRequestMethodKey.String(r.Method),
			semconv.URLPath(r.URL.Path),
			semconv.URLScheme(r.URL.Scheme),
			semconv.ServerAddress(r.Host),
			semconv.NetworkProtocolVersion(r.Proto),
		)
		if ua := r.UserAgent(); ua != "" {
			span.SetAttributes(semconv.UserAgentNameKey.String(ua))
		}
		if addr := r.RemoteAddr; addr != "" {
			span.SetAttributes(semconv.ClientAddressKey.String(addr))
		}

		// Wrap response writer to capture the status code.
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}

		// Write W3C Trace Context response headers
		propagator.Inject(ctx, propagation.HeaderCarrier(w.Header()))

		// Write legacy X-Trace-Id header for backward compatibility.
		traceID := legacyTraceID(ctx, legacyID)
		w.Header().Set("X-Trace-Id", traceID)

		// Inject the trace ID into the legacy context key for event bus consumers.
		ctx = WithTraceID(ctx, traceID)

		next.ServeHTTP(rec, r.WithContext(ctx))

		// Record the captured status code on the span.
		span.SetAttributes(semconv.HTTPResponseStatusCode(rec.status))
		if rec.status >= 400 {
			span.SetStatus(codes.Error, "HTTP "+strconv.Itoa(rec.status))
		}
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
