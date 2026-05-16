package trace

import (
	"context"

	"github.com/Muxcore-Media/core/pkg/contracts"
)

var noopSpanInstance = &noopSpan{}

// noopSpan implements contracts.Span with zero operations.
// End is idempotent for safety.
type noopSpan struct{ ended bool }

func (s *noopSpan) End()                             { s.ended = true }
func (s *noopSpan) SetAttribute(key string, value string) {}
func (s *noopSpan) SetAttributeInt(key string, value int64) {}
func (s *noopSpan) SetAttributeFloat(key string, value float64) {}
func (s *noopSpan) RecordError(err error)             {}
func (s *noopSpan) AddEvent(name string, attrs map[string]any) {}
func (s *noopSpan) SetStatus(code contracts.SpanStatusCode, description string) {}

// NoopTracer implements contracts.Tracer without creating real spans.
// Start always returns the singleton noopSpanInstance — zero heap allocation.
type NoopTracer struct{}

func (t *NoopTracer) Start(ctx context.Context, name string, kind contracts.SpanKind) (contracts.Span, context.Context) {
	return noopSpanInstance, ctx
}

func (t *NoopTracer) Sub(name string) contracts.Tracer {
	return t
}

// NewNoopTracer returns a Tracer that produces noop spans.
// Safe for concurrent use.
func NewNoopTracer() contracts.Tracer {
	return &NoopTracer{}
}
