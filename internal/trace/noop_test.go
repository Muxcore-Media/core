package trace

import (
	"context"
	"testing"

	"github.com/Muxcore-Media/core/pkg/contracts"
)

func TestNoopTracer_Start(t *testing.T) {
	tracer := NewNoopTracer()
	ctx := context.Background()

	span, ctx2 := tracer.Start(ctx, "test.op", contracts.SpanKindInternal)
	if span == nil {
		t.Fatal("Start returned nil span")
	}
	if ctx2 != ctx {
		t.Error("Start should return the same context for noop tracer")
	}

	// Verify the span is the singleton instance.
	span2, _ := tracer.Start(ctx, "test.op2", contracts.SpanKindServer)
	if span != span2 {
		t.Error("noop spans should be the same singleton instance")
	}
}

func TestNoopSpan_IdempotentEnd(t *testing.T) {
	span := &noopSpan{}
	span.End()
	span.End() // should not panic
}

func TestNoopSpan_Methods(t *testing.T) {
	span := &noopSpan{}
	// All methods should be safe to call and not panic.
	span.SetAttribute("key", "value")
	span.SetAttributeInt("count", 42)
	span.SetAttributeFloat("ratio", 0.5)
	span.RecordError(nil)
	span.AddEvent("event", map[string]any{"k": "v"})
	span.SetStatus(contracts.SpanStatusOK, "ok")
	span.SetStatus(contracts.SpanStatusError, "error")
	span.End()
}

func TestNoopTracer_Sub(t *testing.T) {
	tracer := NewNoopTracer()
	sub := tracer.Sub("indexer")

	// Sub on noop should return the same noop tracer.
	if sub != tracer {
		t.Error("Sub on noop tracer should return the same instance")
	}
}

func TestNewNoopTracer_AlwaysNonNil(t *testing.T) {
	tracer := NewNoopTracer()
	if tracer == nil {
		t.Fatal("NewNoopTracer should never return nil")
	}
}
