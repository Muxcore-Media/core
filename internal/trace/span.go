package trace

import (
	"context"
	"log/slog"
	"time"

	"github.com/google/uuid"
)

type spanKey struct{}

// Span holds identifiers for a single unit of work within a distributed trace.
type Span struct {
	TraceID string
	SpanID  string
	Name    string
	start   time.Time
}

// StartSpan begins a child span, preserving the trace ID from ctx and
// generating a new span ID. When no trace ID exists, one is created.
func StartSpan(ctx context.Context, name string) (context.Context, *Span) {
	traceID := FromContext(ctx)
	if traceID == "" {
		traceID = uuid.New().String()
	}
	span := &Span{
		TraceID: traceID,
		SpanID:  uuid.New().String(),
		Name:    name,
		start:   time.Now(),
	}
	ctx = WithTraceID(ctx, traceID)
	ctx = context.WithValue(ctx, spanKey{}, span)
	return ctx, span
}

// SpanFromContext returns the active span, or nil.
func SpanFromContext(ctx context.Context) *Span {
	if ctx == nil {
		return nil
	}
	if s, ok := ctx.Value(spanKey{}).(*Span); ok {
		return s
	}
	return nil
}

// End logs span completion with duration. Safe to call on nil receivers via helper.
func (s *Span) End(err error) {
	if s == nil {
		return
	}
	duration := time.Since(s.start)
	attrs := []any{
		"trace_id", s.TraceID,
		"span_id", s.SpanID,
		"span", s.Name,
		"duration", duration,
	}
	if err != nil {
		attrs = append(attrs, "error", err)
		slog.Warn("span completed with error", attrs...)
		return
	}
	slog.Debug("span completed", attrs...)
}

// ModuleCallSpan starts a span for an inter-module RPC call.
func ModuleCallSpan(ctx context.Context, targetModule, method string) (context.Context, *Span) {
	return StartSpan(ctx, "module:"+targetModule+":"+method)
}
