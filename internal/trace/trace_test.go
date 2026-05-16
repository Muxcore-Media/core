package trace

import (
	"context"
	"testing"
)

func TestFromContext_Empty(t *testing.T) {
	ctx := context.Background()
	if id := FromContext(ctx); id != "" {
		t.Errorf("FromContext on empty context should return empty string, got %q", id)
	}
}

func TestWithTraceID_RoundTrip(t *testing.T) {
	ctx := WithTraceID(context.Background(), "abc123")
	if id := FromContext(ctx); id != "abc123" {
		t.Errorf("FromContext after WithTraceID: got %q, want %q", id, "abc123")
	}
}

func TestNewContext_GeneratesUUID(t *testing.T) {
	ctx := NewContext(context.Background())
	id := FromContext(ctx)
	if id == "" {
		t.Error("NewContext should generate a non-empty trace ID")
	}
	// UUID format check: 36 chars with dashes
	if len(id) != 36 {
		t.Errorf("expected UUID length 36, got %d (%q)", len(id), id)
	}
}

func TestWithTraceID_Overwrite(t *testing.T) {
	ctx := WithTraceID(context.Background(), "first")
	ctx = WithTraceID(ctx, "second")
	if id := FromContext(ctx); id != "second" {
		t.Errorf("WithTraceID should overwrite: got %q, want %q", id, "second")
	}
}
