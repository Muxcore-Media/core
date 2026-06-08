package events

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/Muxcore-Media/core/pkg/contracts"
)

// permissivePolicy allows all event publication for testing.
type permissivePolicy struct{}

func (permissivePolicy) CanPublish(_ context.Context, _, _ string) (bool, error) { return true, nil }

// ---------------------------------------------------------------------------
// Subscribe / Publish
// ---------------------------------------------------------------------------

func TestPublishSubscribe(t *testing.T) {
	bus := NewMemoryBus()
	bus.SetPublishPolicy(permissivePolicy{})

	received := make(chan contracts.Event, 1)
	if _, err := bus.Subscribe(context.Background(), "test.event",
		func(_ context.Context, e contracts.Event) error {
			received <- e
			return nil
		},
	); err != nil {
		t.Fatalf("Subscribe should succeed: %v", err)
	}

	if err := bus.Publish(context.Background(), contracts.Event{Type: "test.event", Payload: []byte(`{"hello":"world"}`)}); err != nil {
		t.Fatalf("Publish should succeed: %v", err)
	}

	select {
	case e := <-received:
		if e.Type != "test.event" {
			t.Fatalf("expected event type test.event, got %s", e.Type)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for event")
	}
}

func TestPublishSubscribeMultipleEvents(t *testing.T) {
	bus := NewMemoryBus()
	bus.SetPublishPolicy(permissivePolicy{})

	received := make(chan struct{}, 3)
	if _, err := bus.Subscribe(context.Background(), "test.event",
		func(_ context.Context, e contracts.Event) error {
			received <- struct{}{}
			return nil
		},
	); err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 3; i++ {
		if err := bus.Publish(context.Background(), contracts.Event{Type: "test.event"}); err != nil {
			t.Fatal(err)
		}
	}

	for i := 0; i < 3; i++ {
		select {
		case <-received:
		case <-time.After(time.Second):
			t.Fatal("timed out waiting for all events")
		}
	}
}

// ---------------------------------------------------------------------------
// Wildcard subscription
// ---------------------------------------------------------------------------

func TestWildcardSubscribe(t *testing.T) {
	bus := NewMemoryBus()
	bus.SetPublishPolicy(permissivePolicy{})

	received := make(chan contracts.Event, 2)
	if _, err := bus.Subscribe(context.Background(), "*",
		func(_ context.Context, e contracts.Event) error {
			received <- e
			return nil
		},
	); err != nil {
		t.Fatal(err)
	}

	if err := bus.Publish(context.Background(), contracts.Event{Type: "event1"}); err != nil {
		t.Fatal(err)
	}
	if err := bus.Publish(context.Background(), contracts.Event{Type: "event2"}); err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 2; i++ {
		select {
		case e := <-received:
			if e.Type != "event1" && e.Type != "event2" {
				t.Fatalf("unexpected event type %s", e.Type)
			}
		case <-time.After(time.Second):
			t.Fatal("timed out waiting for event")
		}
	}
}

func TestWildcardAndSpecificSubscribe(t *testing.T) {
	bus := NewMemoryBus()
	bus.SetPublishPolicy(permissivePolicy{})

	var mu sync.Mutex
	var muWild, muSpecific []string

	wildDone := make(chan struct{})
	specDone := make(chan struct{})

	bus.Subscribe(context.Background(), "*",
		func(_ context.Context, e contracts.Event) error {
			mu.Lock()
			muWild = append(muWild, e.Type)
			got := len(muWild)
			mu.Unlock()
			if got == 2 {
				close(wildDone)
			}
			return nil
		},
	)
	bus.Subscribe(context.Background(), "specific",
		func(_ context.Context, e contracts.Event) error {
			mu.Lock()
			muSpecific = append(muSpecific, e.Type)
			mu.Unlock()
			close(specDone)
			return nil
		},
	)

	bus.Publish(context.Background(), contracts.Event{Type: "specific"})
	bus.Publish(context.Background(), contracts.Event{Type: "other"})

	// Both wildcard handlers should fire for both events.
	select {
	case <-wildDone:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for wildcard handler")
	}

	// The specific handler should only fire for the "specific" event.
	select {
	case <-specDone:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for specific handler")
	}

	mu.Lock()
	if len(muSpecific) != 1 {
		t.Fatalf("expected specific handler to fire once, got %d", len(muSpecific))
	}
	mu.Unlock()
}

// ---------------------------------------------------------------------------
// Cancel (subscription removal via returned cancel func)
// ---------------------------------------------------------------------------

func TestCancelSubscription(t *testing.T) {
	bus := NewMemoryBus()
	bus.SetPublishPolicy(permissivePolicy{})

	called := false
	cancel, err := bus.Subscribe(context.Background(), "test.event",
		func(_ context.Context, e contracts.Event) error {
			called = true
			return nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	cancel()

	if err := bus.Publish(context.Background(), contracts.Event{Type: "test.event"}); err != nil {
		t.Fatal(err)
	}
	// Give any lingering goroutines a chance to run (none should).
	time.Sleep(10 * time.Millisecond)
	if called {
		t.Fatal("handler should not have been called after cancel")
	}
}

func TestCancelIdempotent(t *testing.T) {
	bus := NewMemoryBus()
	bus.SetPublishPolicy(permissivePolicy{})

	cancel, err := bus.Subscribe(context.Background(), "test.event",
		func(_ context.Context, _ contracts.Event) error { return nil },
	)
	if err != nil {
		t.Fatal(err)
	}
	// Calling cancel multiple times must not panic.
	cancel()
	cancel()
}

func TestCancelOnlyRemovesTargetSubscription(t *testing.T) {
	bus := NewMemoryBus()
	bus.SetPublishPolicy(permissivePolicy{})

	received := make(chan contracts.Event, 1)
	_, err := bus.Subscribe(context.Background(), "test.event",
		func(_ context.Context, e contracts.Event) error {
			received <- e
			return nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	// Subscribe a second handler and cancel only it.
	cancel2, err := bus.Subscribe(context.Background(), "test.event",
		func(_ context.Context, _ contracts.Event) error { return nil },
	)
	if err != nil {
		t.Fatal(err)
	}
	cancel2()

	if err := bus.Publish(context.Background(), contracts.Event{Type: "test.event"}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-received:
		// first handler was correctly not removed
	case <-time.After(time.Second):
		t.Fatal("first handler was incorrectly removed")
	}
}

// ---------------------------------------------------------------------------
// Request / Reply
// ---------------------------------------------------------------------------

func TestRequestReply(t *testing.T) {
	bus := NewMemoryBus()
	bus.SetPublishPolicy(permissivePolicy{})

	// Handler that responds to "test.req" with a reply event.
	if _, err := bus.Subscribe(context.Background(), "test.req",
		func(ctx context.Context, e contracts.Event) error {
			replyPayload, _ := json.Marshal(map[string]string{"result": "ok"})
			return bus.Publish(ctx, contracts.Event{
				Type:    "test.req.reply",
				Payload: replyPayload,
			})
		},
	); err != nil {
		t.Fatal(err)
	}

	reply, err := bus.Request(context.Background(), contracts.Event{Type: "test.req"}, time.Second)
	if err != nil {
		t.Fatalf("Request should succeed: %v", err)
	}
	if reply.Type != "test.req.reply" {
		t.Fatalf("expected reply type test.req.reply, got %s", reply.Type)
	}

	var payload map[string]string
	if err := json.Unmarshal(reply.Payload, &payload); err != nil {
		t.Fatalf("failed to unmarshal reply payload: %v", err)
	}
	if payload["result"] != "ok" {
		t.Fatalf("expected result=ok, got %v", payload)
	}
}

// ---------------------------------------------------------------------------
// Request timeout
// ---------------------------------------------------------------------------

func TestRequestTimeout(t *testing.T) {
	bus := NewMemoryBus()
	bus.SetPublishPolicy(permissivePolicy{})
	// No handler subscribed, so no reply will come.
	_, err := bus.Request(context.Background(), contracts.Event{Type: "test.req"}, time.Millisecond)
	if err == nil {
		t.Fatal("expected timeout error")
	}
}

// ---------------------------------------------------------------------------
// Request context cancellation
// ---------------------------------------------------------------------------

func TestRequestContextCancel(t *testing.T) {
	bus := NewMemoryBus()
	bus.SetPublishPolicy(permissivePolicy{})
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	_, err := bus.Request(ctx, contracts.Event{Type: "test.req"}, time.Second)
	if err == nil {
		t.Fatal("expected context cancellation error")
	}
}

// ---------------------------------------------------------------------------
// Multiple subscribers
// ---------------------------------------------------------------------------

func TestMultipleSubscribers(t *testing.T) {
	bus := NewMemoryBus()
	bus.SetPublishPolicy(permissivePolicy{})

	const numHandlers = 5
	received := make(chan int, numHandlers)

	for i := 0; i < numHandlers; i++ {
		id := i
		if _, err := bus.Subscribe(context.Background(), "test.event",
			func(_ context.Context, e contracts.Event) error {
				received <- id
				return nil
			},
		); err != nil {
			t.Fatal(err)
		}
	}

	if err := bus.Publish(context.Background(), contracts.Event{Type: "test.event"}); err != nil {
		t.Fatal(err)
	}

	seen := make(map[int]bool)
	for i := 0; i < numHandlers; i++ {
		select {
		case id := <-received:
			seen[id] = true
		case <-time.After(time.Second):
			t.Fatalf("timed out waiting for handler %d of %d", i+1, numHandlers)
		}
	}

	if len(seen) != numHandlers {
		t.Fatalf("expected %d unique handlers to fire, got %d", numHandlers, len(seen))
	}
}

// ---------------------------------------------------------------------------
// Auto-generated ID and Timestamp
// ---------------------------------------------------------------------------

func TestAutoIDTimestamp(t *testing.T) {
	bus := NewMemoryBus()
	bus.SetPublishPolicy(permissivePolicy{})

	received := make(chan contracts.Event, 1)
	bus.Subscribe(context.Background(), "test",
		func(_ context.Context, e contracts.Event) error {
			received <- e
			return nil
		},
	)

	bus.Publish(context.Background(), contracts.Event{Type: "test"})

	select {
	case e := <-received:
		if e.ID == "" {
			t.Fatal("expected auto-generated ID")
		}
		if e.Timestamp.IsZero() {
			t.Fatal("expected auto-generated timestamp")
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for event")
	}
}

func TestPreservesProvidedIDTimestamp(t *testing.T) {
	bus := NewMemoryBus()
	bus.SetPublishPolicy(permissivePolicy{})

	received := make(chan contracts.Event, 1)
	bus.Subscribe(context.Background(), "test",
		func(_ context.Context, e contracts.Event) error {
			received <- e
			return nil
		},
	)

	now := time.Date(2026, 5, 15, 12, 0, 0, 0, time.UTC)
	bus.Publish(context.Background(), contracts.Event{
		Type:      "test",
		ID:        "my-custom-id",
		Timestamp: now,
	})

	select {
	case e := <-received:
		if e.ID != "my-custom-id" {
			t.Fatalf("expected ID my-custom-id, got %s", e.ID)
		}
		if !e.Timestamp.Equal(now) {
			t.Fatalf("expected timestamp %v, got %v", now, e.Timestamp)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for event")
	}
}
