package trace

import (
	"context"
	"fmt"

	"github.com/Muxcore-Media/core/pkg/contracts"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	oteltrace "go.opentelemetry.io/otel/trace"
)

// otelSpan wraps an OpenTelemetry span to implement contracts.Span.
// End is idempotent — calling it multiple times is safe.
type otelSpan struct {
	span  oteltrace.Span
	ended bool
}

func (s *otelSpan) End() {
	if s.ended {
		return
	}
	s.ended = true
	s.span.End()
}

func (s *otelSpan) SetAttribute(key string, value string) {
	s.span.SetAttributes(attribute.String(key, value))
}

func (s *otelSpan) SetAttributeInt(key string, value int64) {
	s.span.SetAttributes(attribute.Int64(key, value))
}

func (s *otelSpan) SetAttributeFloat(key string, value float64) {
	s.span.SetAttributes(attribute.Float64(key, value))
}

func (s *otelSpan) RecordError(err error) {
	s.span.RecordError(err)
}

func (s *otelSpan) AddEvent(name string, attrs map[string]any) {
	opts := []oteltrace.EventOption{}
	for k, v := range attrs {
		opts = append(opts, oteltrace.WithAttributes(attributeAny(k, v)))
	}
	s.span.AddEvent(name, opts...)
}

func (s *otelSpan) SetStatus(code contracts.SpanStatusCode, description string) {
	switch code {
	case contracts.SpanStatusOK:
		s.span.SetStatus(codes.Ok, description)
	case contracts.SpanStatusError:
		s.span.SetStatus(codes.Error, description)
	}
}

// attributeAny converts an arbitrary value to an OTEL attribute.KeyValue.
// Only types supported by the OTEL specification are preserved;
// unsupported types become a string via fmt.Sprintf.
func attributeAny(key string, v any) attribute.KeyValue {
	switch val := v.(type) {
	case string:
		return attribute.String(key, val)
	case int:
		return attribute.Int64(key, int64(val))
	case int64:
		return attribute.Int64(key, val)
	case float64:
		return attribute.Float64(key, val)
	case bool:
		return attribute.Bool(key, val)
	default:
		return attribute.String(key, fmt.Sprintf("%v", val))
	}
}

// otelLibTracer wraps the OpenTelemetry Tracer to implement contracts.Tracer.
type otelLibTracer struct {
	tracer oteltrace.Tracer
	prefix *string // non-nil when this is a Sub-tracer
}

func newOTELLibTracer(tracer oteltrace.Tracer, prefix *string) *otelLibTracer {
	return &otelLibTracer{tracer: tracer, prefix: prefix}
}

func (t *otelLibTracer) Start(ctx context.Context, name string, kind contracts.SpanKind) (contracts.Span, context.Context) {
	if t.prefix != nil {
		name = *t.prefix + "." + name
	}
	otelKind := spanKindToOTEL(kind)
	ctx, span := t.tracer.Start(ctx, name, oteltrace.WithSpanKind(otelKind))
	return &otelSpan{span: span}, ctx
}

func (t *otelLibTracer) Sub(name string) contracts.Tracer {
	prefix := name
	if t.prefix != nil {
		prefix = *t.prefix + "." + name
	}
	return &otelLibTracer{tracer: t.tracer, prefix: &prefix}
}

func spanKindToOTEL(k contracts.SpanKind) oteltrace.SpanKind {
	switch k {
	case contracts.SpanKindServer:
		return oteltrace.SpanKindServer
	case contracts.SpanKindClient:
		return oteltrace.SpanKindClient
	case contracts.SpanKindProducer:
		return oteltrace.SpanKindProducer
	case contracts.SpanKindConsumer:
		return oteltrace.SpanKindConsumer
	default:
		return oteltrace.SpanKindInternal
	}
}
