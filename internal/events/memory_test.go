package events

import (
	"context"
	"encoding/json"
	"io"
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

func TestSubscriberCount(t *testing.T) {
	bus := NewMemoryBus()
	bus.SetPublishPolicy(permissivePolicy{})

	if c := bus.SubscriberCount(); c != 0 {
		t.Errorf("expected 0 subscribers initially, got %d", c)
	}

	bus.Subscribe(context.Background(), "evt.a", func(_ context.Context, e contracts.Event) error { return nil })
	bus.Subscribe(context.Background(), "evt.b", func(_ context.Context, e contracts.Event) error { return nil })

	if c := bus.SubscriberCount(); c != 2 {
		t.Errorf("expected 2 subscribers, got %d", c)
	}
}

func TestPublishCount(t *testing.T) {
	bus := NewMemoryBus()
	bus.SetPublishPolicy(permissivePolicy{})

	if c := bus.PublishCount(); c != 0 {
		t.Errorf("expected 0 initially, got %d", c)
	}

	bus.Publish(context.Background(), contracts.Event{Type: "test"})
	bus.Publish(context.Background(), contracts.Event{Type: "test"})
	bus.Publish(context.Background(), contracts.Event{Type: "test"})

	if c := bus.PublishCount(); c != 3 {
		t.Errorf("expected 3 publishes, got %d", c)
	}
}

func TestDroppedEvents_Initial(t *testing.T) {
	bus := NewMemoryBus()
	if c := bus.DroppedEvents(); c != 0 {
		t.Errorf("expected 0 dropped events initially, got %d", c)
	}
}

func TestMemoryBus_CloseWithDrain(t *testing.T) {
	bus := NewMemoryBus()
	bus.SetPublishPolicy(allowAllPolicy{})

	var mu sync.Mutex
	var processed int

	handler := func(ctx context.Context, e contracts.Event) error {
		mu.Lock()
		processed++
		mu.Unlock()
		time.Sleep(10 * time.Millisecond)
		return nil
	}

	bus.Subscribe(context.Background(), "test.event", handler)

	for i := 0; i < 3; i++ {
		bus.Publish(context.Background(), contracts.Event{
			Type: "test.event", Source: "test",
		})
	}

	bus.Close()

	_, err := bus.Subscribe(context.Background(), "other.event", handler)
	if err == nil {
		t.Error("expected error subscribing after close")
	}

	mu.Lock()
	if processed != 3 {
		t.Errorf("expected 3 events processed, got %d", processed)
	}
	mu.Unlock()
}

func TestMemoryBus_Close_WithWAL(t *testing.T) {
	dir := t.TempDir()
	bus := NewMemoryBus()
	bus.SetPublishPolicy(allowAllPolicy{})

	if err := bus.EnableWAL(dir); err != nil {
		t.Fatalf("EnableWAL: %v", err)
	}

	bus.Subscribe(context.Background(), "test.event",
		func(_ context.Context, e contracts.Event) error { return nil },
	)

	bus.Publish(context.Background(), contracts.Event{Type: "test.event", Source: "test"})

	bus.Close()
}

func TestMemoryBus_Close_BlocksSubscribe(t *testing.T) {
	bus := NewMemoryBus()
	bus.Close()

	_, err := bus.Subscribe(context.Background(), "test", func(_ context.Context, e contracts.Event) error {
		return nil
	})
	if err == nil {
		t.Error("expected Subscribe to fail after Close")
	}
}

func TestMemoryBus_Close_Idempotent(t *testing.T) {
	bus := NewMemoryBus()
	bus.Close()
	bus.Close() // must not panic
}

func TestMemoryBus_SubscribeModule(t *testing.T) {
	bus := NewMemoryBus()
	bus.SetPublishPolicy(permissivePolicy{})

	received := make(chan contracts.Event, 1)
	_, err := bus.SubscribeModule(context.Background(), "mod-a", "test.event",
		func(_ context.Context, e contracts.Event) error {
			received <- e
			return nil
		},
	)
	if err != nil {
		t.Fatalf("SubscribeModule: %v", err)
	}

	bus.Publish(context.Background(), contracts.Event{Type: "test.event"})

	select {
	case <-received:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for event via SubscribeModule")
	}
}

func TestMemoryBus_UnsubscribeAll(t *testing.T) {
	bus := NewMemoryBus()
	bus.SetPublishPolicy(permissivePolicy{})

	var count int
	bus.SubscribeModule(context.Background(), "mod-a", "evt",
		func(_ context.Context, e contracts.Event) error { count++; return nil },
	)
	bus.SubscribeModule(context.Background(), "mod-a", "evt",
		func(_ context.Context, e contracts.Event) error { count++; return nil },
	)
	bus.SubscribeModule(context.Background(), "mod-b", "evt",
		func(_ context.Context, e contracts.Event) error { count++; return nil },
	)

	if c := bus.SubscriberCount(); c != 3 {
		t.Fatalf("expected 3 subscribers before UnsubscribeAll, got %d", c)
	}

	bus.UnsubscribeAll(context.Background(), "mod-a")
	if c := bus.SubscriberCount(); c != 1 {
		t.Errorf("expected 1 subscriber after UnsubscribeAll(mod-a), got %d", c)
	}
}

func TestMemoryBus_PublishPolicy(t *testing.T) {
	bus := NewMemoryBus()
	policy := permissivePolicy{}

	// Should start as nil.
	if bus.PublishPolicy() != nil {
		t.Error("expected nil PublishPolicy initially")
	}

	bus.SetPublishPolicy(policy)
	if bus.PublishPolicy() != policy {
		t.Error("expected PublishPolicy to return the set policy")
	}
}

func TestMemoryBus_SetAuditLogger_NoPanic(t *testing.T) {
	bus := NewMemoryBus()
	bus.SetAuditLogger(&mockAuditLogger{})
	bus.SetNodeID("node-1")
}

func TestMemoryBus_SubscriptionStats(t *testing.T) {
	bus := NewMemoryBus()
	bus.SetPublishPolicy(permissivePolicy{})

	bus.Subscribe(context.Background(), "evt.a", func(_ context.Context, e contracts.Event) error { return nil })
	bus.Subscribe(context.Background(), "evt.a", func(_ context.Context, e contracts.Event) error { return nil })
	bus.Subscribe(context.Background(), "evt.b", func(_ context.Context, e contracts.Event) error { return nil })

	stats := bus.SubscriptionStats()
	if len(stats) != 2 {
		t.Fatalf("expected 2 event types in stats, got %d: %v", len(stats), stats)
	}
	for _, s := range stats {
		switch s.EventType {
		case "evt.a":
			if s.SubscriberCount != 2 {
				t.Errorf("expected 2 subscribers for evt.a, got %d", s.SubscriberCount)
			}
		case "evt.b":
			if s.SubscriberCount != 1 {
				t.Errorf("expected 1 subscriber for evt.b, got %d", s.SubscriberCount)
			}
		default:
			t.Errorf("unexpected event type %q in stats", s.EventType)
		}
	}
}

func TestMemoryBus_SubscriberStats_AfterPublish(t *testing.T) {
	bus := NewMemoryBus()
	bus.SetPublishPolicy(permissivePolicy{})

	bus.Subscribe(context.Background(), "evt.a", func(_ context.Context, e contracts.Event) error { return nil })
	bus.Publish(context.Background(), contracts.Event{Type: "evt.a"})
	bus.Publish(context.Background(), contracts.Event{Type: "evt.a"})
	time.Sleep(50 * time.Millisecond)

	stats := bus.SubscriberStats()
	if len(stats) == 0 {
		t.Fatal("expected subscriber stats after publish")
	}
	if stats[0].Processed == 0 {
		t.Errorf("expected processed count > 0 after publish, got %d", stats[0].Processed)
	}
}

func TestMemoryBus_ReplayFrom_WithoutWAL(t *testing.T) {
	bus := NewMemoryBus()
	err := bus.ReplayFrom(context.Background(), 0, func(e contracts.Event) error { return nil })
	if err == nil {
		t.Error("expected error when ReplayFrom called without WAL")
	}
}

// mockAuditLogger implements contracts.AuditLogger for testing.
type mockAuditLogger struct{}

func (m *mockAuditLogger) Log(_ context.Context, _ contracts.AuditEntry) error { return nil }
func (m *mockAuditLogger) Query(_ context.Context, _ contracts.AuditFilter) ([]contracts.AuditEntry, error) {
	return nil, nil
}
func (m *mockAuditLogger) Export(_ context.Context, _ string) (io.ReadCloser, error) { return nil, nil }
func (m *mockAuditLogger) VerifyChainIntegrity(_ context.Context, _, _ time.Time) (contracts.ChainVerificationResult, error) {
	return contracts.ChainVerificationResult{Valid: true}, nil
}
func (m *mockAuditLogger) VerifyAll(_ context.Context) (contracts.ChainVerificationResult, error) {
	return contracts.ChainVerificationResult{Valid: true}, nil
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
