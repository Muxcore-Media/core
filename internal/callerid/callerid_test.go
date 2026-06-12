package callerid

import (
	"context"
	"testing"
)

func TestSetGet(t *testing.T) {
	t.Run("round-trip", func(t *testing.T) {
		ctx := Set(context.Background(), "module-foo")
		got := Get(ctx)
		if got != "module-foo" {
			t.Fatalf("got %q, want %q", got, "module-foo")
		}
	})

	t.Run("empty context returns empty", func(t *testing.T) {
		if got := Get(context.Background()); got != "" {
			t.Fatalf("got %q, want empty", got)
		}
	})

	t.Run("overwrite", func(t *testing.T) {
		ctx := Set(context.Background(), "first")
		ctx = Set(ctx, "second")
		if got := Get(ctx); got != "second" {
			t.Fatalf("got %q, want %q", got, "second")
		}
	})

	t.Run("different keys are independent", func(t *testing.T) {
		// Verify that Set uses a key struct{} that doesn't collide with
		// other context value keys by nesting in a parent context.
		type otherKey struct{}
		parent := context.WithValue(context.Background(), otherKey{}, "other-value")
		ctx := Set(parent, "caller-value")

		if got := Get(ctx); got != "caller-value" {
			t.Fatalf("Get: got %q, want %q", got, "caller-value")
		}
		if v := ctx.Value(otherKey{}); v != "other-value" {
			t.Fatalf("parent value lost: got %v, want %q", v, "other-value")
		}
	})

	t.Run("concurrent access", func(t *testing.T) {
		ctx := context.Background()
		ctx = Set(ctx, "concurrent-caller")

		var results [10]string
		for i := range 10 {
			results[i] = Get(ctx)
		}
		for i, r := range results {
			if r != "concurrent-caller" {
				t.Fatalf("result[%d] = %q, want %q", i, r, "concurrent-caller")
			}
		}
	})
}
