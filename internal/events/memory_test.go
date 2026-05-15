package events

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/Muxcore-Media/core/pkg/contracts"
)

// ---------------------------------------------------------------------------
// Subscribe / Publish
// ---------------------------------------------------------------------------

func TestPublishSubscribe(t *testing.T) {
	bus := NewMemoryBus()

	received := make(chan contracts.Event, 1)
	if err := bus.Subscribe(context.Background(), "test.event",
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

	received := make(chan struct{}, 3)
	if err := bus.Subscribe(context.Background(), "test.event",
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

	received := make(chan contracts.Event, 2)
	if err := bus.Subscribe(context.Background(), "*",
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
// Unsubscribe
// ---------------------------------------------------------------------------

func TestUnsubscribe(t *testing.T) {
	bus := NewMemoryBus()

	called := false
	handler := func(_ context.Context, e contracts.Event) error {
		called = true
		return nil
	}

	if err := bus.Subscribe(context.Background(), "test.event", handler); err != nil {
		t.Fatal(err)
	}
	if err := bus.Unsubscribe(context.Background(), "test.event", handler); err != nil {
		t.Fatal(err)
	}

	if err := bus.Publish(context.Background(), contracts.Event{Type: "test.event"}); err != nil {
		t.Fatal(err)
	}

	// Give any lingering goroutines a chance to run (none should).
	time.Sleep(10 * time.Millisecond)

	if called {
		t.Fatal("handler should not have been called after unsubscribe")
	}
}

func TestUnsubscribeNoopForWrongType(t *testing.T) {
	bus := NewMemoryBus()

	received := make(chan contracts.Event, 1)
	handler := func(_ context.Context, e contracts.Event) error {
		received <- e
		return nil
	}

	bus.Subscribe(context.Background(), "test.event", handler)
	// Unsubscribe from a different type — handler should remain.
	bus.Unsubscribe(context.Background(), "other", handler)

	bus.Publish(context.Background(), contracts.Event{Type: "test.event"})

	select {
	case <-received:
		// handler was correctly not removed
	case <-time.After(time.Second):
		t.Fatal("handler was incorrectly removed")
	}
}

func TestUnsubscribeNoopForDifferentHandler(t *testing.T) {
	bus := NewMemoryBus()

	received := make(chan contracts.Event, 1)
	handler := func(_ context.Context, e contracts.Event) error {
		received <- e
		return nil
	}
	otherHandler := func(_ context.Context, e contracts.Event) error {
		return nil
	}

	bus.Subscribe(context.Background(), "test.event", handler)
	// Unsubscribe with a different handler reference — should NOT remove.
	bus.Unsubscribe(context.Background(), "test.event", otherHandler)

	bus.Publish(context.Background(), contracts.Event{Type: "test.event"})

	select {
	case <-received:
		// handler was correctly not removed
	case <-time.After(time.Second):
		t.Fatal("handler was incorrectly removed")
	}
}

// ---------------------------------------------------------------------------
// Request / Reply
// ---------------------------------------------------------------------------

func TestRequestReply(t *testing.T) {
	bus := NewMemoryBus()

	// Handler that responds to "test.req" with a reply event.
	if err := bus.Subscribe(context.Background(), "test.req",
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
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	_, err := bus.Request(ctx, contracts.Event{Type: "test.req"}, time.Second)
	if err == nil {
		t.Fatal("expected context cancellation error")
	}
}

// ---------------------------------------------------------------------------
// Event validation
// ---------------------------------------------------------------------------

func TestValidationValidPayload(t *testing.T) {
	bus := NewMemoryBus()
	bus.SetValidation(true)

	validPayload, _ := json.Marshal(contracts.MediaRequestedPayload{
		MediaType: "movie",
		Title:     "Test Movie",
	})

	if err := bus.Publish(context.Background(), contracts.Event{
		Type:    contracts.EventMediaRequested,
		Payload: validPayload,
	}); err != nil {
		t.Fatalf("publish with valid payload should succeed: %v", err)
	}
}

func TestValidationInvalidPayload(t *testing.T) {
	bus := NewMemoryBus()
	bus.SetValidation(true)

	if err := bus.Publish(context.Background(), contracts.Event{
		Type:    contracts.EventMediaRequested,
		Payload: []byte("not-valid-json"),
	}); err == nil {
		t.Fatal("expected publish with invalid payload to fail")
	}
}

func TestValidationEmptyPayload(t *testing.T) {
	bus := NewMemoryBus()
	bus.SetValidation(true)

	// An empty payload is not valid JSON for a struct.
	if err := bus.Publish(context.Background(), contracts.Event{
		Type:    contracts.EventMediaRequested,
		Payload: []byte{},
	}); err == nil {
		t.Fatal("expected publish with empty payload to fail")
	}
}

func TestValidationDisabled(t *testing.T) {
	bus := NewMemoryBus()
	// Validation is disabled by default.
	if err := bus.Publish(context.Background(), contracts.Event{
		Type:    contracts.EventMediaRequested,
		Payload: []byte("not-valid-json"),
	}); err != nil {
		t.Fatalf("publish with invalid payload should succeed when validation is disabled: %v", err)
	}
}

func TestValidationUnknownEventType(t *testing.T) {
	bus := NewMemoryBus()
	bus.SetValidation(true)

	// Unknown event types pass through validation.
	if err := bus.Publish(context.Background(), contracts.Event{
		Type:    "custom.event",
		Payload: []byte("any-garbage"),
	}); err != nil {
		t.Fatalf("unknown event types should pass validation: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Multiple subscribers
// ---------------------------------------------------------------------------

func TestMultipleSubscribers(t *testing.T) {
	bus := NewMemoryBus()

	const numHandlers = 5
	received := make(chan int, numHandlers)

	for i := 0; i < numHandlers; i++ {
		id := i
		if err := bus.Subscribe(context.Background(), "test.event",
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
