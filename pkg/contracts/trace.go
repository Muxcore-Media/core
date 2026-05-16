package contracts

import "context"

// SpanKind classifies a span's role in the system.
type SpanKind int

const (
	SpanKindInternal SpanKind = iota
	SpanKindServer
	SpanKindClient
	SpanKindProducer
	SpanKindConsumer
)

// SpanStatusCode indicates whether a span completed successfully.
type SpanStatusCode int

const (
	SpanStatusOK    SpanStatusCode = 0
	SpanStatusError SpanStatusCode = 1
)

// Span represents a single unit of work in a trace.
// Modules create spans via Tracer.Start and defer span.End().
// End is idempotent — calling it multiple times is safe.
type Span interface {
	// End marks the span as complete. Safe to call multiple times.
	End()

	// SetAttribute sets a string attribute on the span.
	SetAttribute(key string, value string)

	// SetAttributeInt sets an int64 attribute on the span.
	SetAttributeInt(key string, value int64)

	// SetAttributeFloat sets a float64 attribute on the span.
	SetAttributeFloat(key string, value float64)

	// RecordError records an error on the span.
	RecordError(err error)

	// AddEvent adds a named event at the current point in the span timeline.
	AddEvent(name string, attrs map[string]any)

	// SetStatus marks the span's final status. If not called, spans default to OK.
	SetStatus(code SpanStatusCode, description string)
}

// Tracer creates spans. Modules receive this via ModuleDeps.
// When tracing is disabled (default), a noop implementation is provided —
// modules can call Start unconditionally with zero overhead.
//
// Span naming convention: "module-name.operation" (e.g., "indexer.search").
// Use Sub() to create a pre-namespaced tracer for a module:
//
//	t := deps.Tracer.Sub("my-module")
//	span, ctx := t.Start(ctx, "search")
type Tracer interface {
	// Start creates a new child span from the parent span in ctx.
	// kind classifies the span's role (internal, server, client, producer, consumer).
	// If ctx contains no parent span, creates a new root span.
	// Returns the span and a context carrying it.
	Start(ctx context.Context, name string, kind SpanKind) (Span, context.Context)

	// Sub returns a namespaced tracer that prefixes span names with name.
	// e.g., tracer.Sub("indexer").Start(ctx, "search", ...) creates span "indexer.search".
	Sub(name string) Tracer
}
