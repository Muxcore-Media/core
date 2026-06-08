package contracts

import "context"

// TracingProvider creates spans for distributed tracing. Modules call this
// to wrap operations in spans; the provider handles export to whatever
// backend is configured (Jaeger, Zipkin, OTLP, or future protocols).
//
// If no TracingProvider is registered, StartSpan returns the context
// unchanged and a no-op span — modules don't need nil checks.
//
// This contract is provider-agnostic by design. A module that creates
// spans through TracingProvider works identically whether the backend
// is OpenTelemetry, a proprietary APM, or a future standard.
type TracingProvider interface {
	// StartSpan begins a new span with the given name as a child of any
	// span in ctx. Returns the context carrying the new span and the span
	// itself. Call span.End() when the operation completes.
	StartSpan(ctx context.Context, name string) (context.Context, Span)
}

// Span represents a single unit of work in a distributed trace.
type Span interface {
	// SetAttribute records a key-value pair on the span. Attributes appear
	// in trace visualizations and can be searched.
	SetAttribute(key string, value string)

	// SetStatus records the outcome of the span. Call before End().
	SetStatus(code SpanStatusCode, description string)

	// End marks the span as complete and sends it to the tracing backend.
	End()
}

// SpanStatusCode indicates success or failure of a span.
type SpanStatusCode int

const (
	SpanStatusOK    SpanStatusCode = 0
	SpanStatusError SpanStatusCode = 1
)
