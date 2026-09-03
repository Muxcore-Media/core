package trace

import (
	"context"
	"testing"

	"google.golang.org/grpc/metadata"
)

func TestStartSpan_PreservesTraceID(t *testing.T) {
	parent := WithTraceID(context.Background(), "parent-trace")
	ctx, span := StartSpan(parent, "child")
	if span.TraceID != "parent-trace" {
		t.Fatalf("trace ID = %q, want parent-trace", span.TraceID)
	}
	if span.SpanID == "" {
		t.Fatal("expected span ID")
	}
	if FromContext(ctx) != "parent-trace" {
		t.Fatalf("context trace = %q", FromContext(ctx))
	}
}

func TestInjectOutgoing_RoundTrip(t *testing.T) {
	ctx, span := StartSpan(context.Background(), "test")
	out := InjectOutgoing(ctx)
	md, ok := metadata.FromOutgoingContext(out)
	if !ok {
		t.Fatal("expected outgoing metadata")
	}
	if got := md.Get(MetadataTraceID); len(got) != 1 || got[0] != span.TraceID {
		t.Fatalf("trace metadata = %v", got)
	}
	if got := md.Get(MetadataSpanID); len(got) != 1 || got[0] != span.SpanID {
		t.Fatalf("span metadata = %v", got)
	}
}

func TestExtractFromIncoming(t *testing.T) {
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs(
		MetadataTraceID, "550e8400-e29b-41d4-a716-446655440000",
		MetadataSpanID, "parent-span",
	))
	ctx = ExtractFromIncoming(ctx)
	if got := FromContext(ctx); got != "550e8400-e29b-41d4-a716-446655440000" {
		t.Fatalf("trace = %q", got)
	}
	if span := SpanFromContext(ctx); span == nil || span.SpanID != "parent-span" {
		t.Fatalf("span = %+v", span)
	}
}
