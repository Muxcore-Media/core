package health

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func TestNew(t *testing.T) {
	ch := New()
	if ch == nil {
		t.Fatal("New() returned nil")
	}
	if ch.ProbeCount() != 0 {
		t.Errorf("expected 0 probes, got %d", ch.ProbeCount())
	}
}

func TestRegisterProbe(t *testing.T) {
	ch := New()
	ch.RegisterProbe("disk", func(ctx context.Context) error { return nil })
	if ch.ProbeCount() != 1 {
		t.Errorf("expected 1 probe, got %d", ch.ProbeCount())
	}
}

func TestRegisterProbe_DuplicateNameIsNoop(t *testing.T) {
	ch := New()
	ch.RegisterProbe("disk", func(ctx context.Context) error { return nil })
	ch.RegisterProbe("disk", func(ctx context.Context) error { return errors.New("second") })
	// Duplicate registration silently drops — should still be 1 probe.
	if ch.ProbeCount() != 1 {
		t.Errorf("expected 1 probe after duplicate registration, got %d", ch.ProbeCount())
	}
}

func TestCheck_AllHealthy(t *testing.T) {
	ch := New()
	ch.RegisterProbe("a", func(ctx context.Context) error { return nil })
	ch.RegisterProbe("b", func(ctx context.Context) error { return nil })

	results := ch.Check(context.Background())
	if len(results) != 2 {
		t.Errorf("expected 2 results, got %d", len(results))
	}
	for name, err := range results {
		if err != nil {
			t.Errorf("probe %q should be healthy, got %v", name, err)
		}
	}
}

func TestCheck_FailingProbe(t *testing.T) {
	ch := New()
	want := errors.New("disk full")
	ch.RegisterProbe("disk", func(ctx context.Context) error { return want })
	ch.RegisterProbe("net", func(ctx context.Context) error { return nil })

	results := ch.Check(context.Background())
	if results["disk"] == nil {
		t.Error("expected disk probe to fail")
	}
	if results["net"] != nil {
		t.Error("expected net probe to pass")
	}
}

func TestCheck_ProbesRunConcurrently(t *testing.T) {
	ch := New()
	var mu sync.Mutex
	var order []string

	// Both probes block briefly; if run sequentially the second would
	// always start after the first finishes. Concurrent execution means
	// both will be "running" at the same time.
	started := make(chan struct{})
	ch.RegisterProbe("a", func(ctx context.Context) error {
		mu.Lock()
		order = append(order, "a-start")
		mu.Unlock()
		close(started)
		time.Sleep(50 * time.Millisecond)
		mu.Lock()
		order = append(order, "a-end")
		mu.Unlock()
		return nil
	})
	ch.RegisterProbe("b", func(ctx context.Context) error {
		<-started // wait until a has started
		mu.Lock()
		order = append(order, "b-start")
		mu.Unlock()
		return nil
	})

	ch.Check(context.Background())

	mu.Lock()
	defer mu.Unlock()
	// b-start should appear before a-end if they ran concurrently.
	aEnd, bStart := -1, -1
	for i, s := range order {
		if s == "a-end" {
			aEnd = i
		}
		if s == "b-start" {
			bStart = i
		}
	}
	if bStart == -1 || aEnd == -1 {
		t.Fatalf("unexpected order: %v", order)
	}
	if bStart > aEnd {
		t.Errorf("probes appear sequential (b-start after a-end): %v", order)
	}
}

func TestCheck_ProbeTimeout(t *testing.T) {
	ch := New()
	ch.ttl = 0 // disable caching for this test

	ch.RegisterProbe("slow", func(ctx context.Context) error {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(10 * time.Second):
			return nil
		}
	})

	// The probe has a 5s internal timeout; our test context is very short.
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	results := ch.Check(ctx)
	if results["slow"] == nil {
		t.Error("expected slow probe to be cancelled")
	}
}

func TestCheck_ResultsCached(t *testing.T) {
	ch := New()
	callCount := 0
	ch.RegisterProbe("counted", func(ctx context.Context) error {
		callCount++
		return nil
	})

	ch.Check(context.Background())
	ch.Check(context.Background())
	// Second call should be served from cache.
	if callCount != 1 {
		t.Errorf("expected probe called once (cache hit), got %d", callCount)
	}
}

func TestCheck_CacheExpires(t *testing.T) {
	ch := New()
	ch.ttl = 10 * time.Millisecond

	callCount := 0
	ch.RegisterProbe("counted", func(ctx context.Context) error {
		callCount++
		return nil
	})

	ch.Check(context.Background())
	time.Sleep(20 * time.Millisecond)
	ch.Check(context.Background())

	if callCount < 2 {
		t.Errorf("expected probe called at least twice after cache expiry, got %d", callCount)
	}
}

func TestHealthy_AllPass(t *testing.T) {
	ch := New()
	ch.RegisterProbe("a", func(ctx context.Context) error { return nil })
	if !ch.Healthy(context.Background()) {
		t.Error("expected Healthy() = true when all probes pass")
	}
}

func TestHealthy_OneFails(t *testing.T) {
	ch := New()
	ch.RegisterProbe("a", func(ctx context.Context) error { return nil })
	ch.RegisterProbe("b", func(ctx context.Context) error { return errors.New("bad") })
	if ch.Healthy(context.Background()) {
		t.Error("expected Healthy() = false when a probe fails")
	}
}

func TestHealthy_NoProbes(t *testing.T) {
	ch := New()
	if !ch.Healthy(context.Background()) {
		t.Error("expected Healthy() = true when no probes registered")
	}
}
