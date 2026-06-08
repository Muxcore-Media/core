package mock

import (
	"context"
	"sync"
	"time"

	"github.com/Muxcore-Media/core/pkg/contracts"
)

// EventBus is a mock event bus for module testing.
type EventBus struct {
	mu            sync.RWMutex
	Published     []contracts.Event
	subs          []*mockSub
	HandlerErrors []error // errors returned by handlers during Publish (cleared on Reset)
}

type mockSub struct {
	moduleID  string
	eventType string
	handler   contracts.EventHandler
}

func NewEventBus() *EventBus {
	return &EventBus{
		Published: make([]contracts.Event, 0),
	}
}

func (b *EventBus) Publish(ctx context.Context, event contracts.Event) error {
	b.mu.RLock()
	var matching []contracts.EventHandler
	for _, s := range b.subs {
		if s.eventType == event.Type || s.eventType == "*" {
			matching = append(matching, s.handler)
		}
	}
	b.mu.RUnlock()

	b.mu.Lock()
	b.Published = append(b.Published, event)
	b.mu.Unlock()

	for _, h := range matching {
		if err := h(ctx, event); err != nil {
			b.mu.Lock()
			b.HandlerErrors = append(b.HandlerErrors, err)
			b.mu.Unlock()
		}
	}
	return nil
}

func (b *EventBus) Subscribe(ctx context.Context, eventType string, handler contracts.EventHandler) (func(), error) {
	s := &mockSub{eventType: eventType, handler: handler}
	b.mu.Lock()
	b.subs = append(b.subs, s)
	b.mu.Unlock()

	return b.cancelFor(s), nil
}

func (b *EventBus) SubscribeModule(ctx context.Context, moduleID, eventType string, handler contracts.EventHandler) (func(), error) {
	s := &mockSub{moduleID: moduleID, eventType: eventType, handler: handler}
	b.mu.Lock()
	b.subs = append(b.subs, s)
	b.mu.Unlock()

	return b.cancelFor(s), nil
}

// cancelFor returns a cancel func that removes exactly the given subscription.
func (b *EventBus) cancelFor(target *mockSub) func() {
	return func() {
		b.mu.Lock()
		defer b.mu.Unlock()
		filtered := b.subs[:0]
		for _, s := range b.subs {
			if s != target {
				filtered = append(filtered, s)
			}
		}
		b.subs = filtered
	}
}

func (b *EventBus) UnsubscribeAll(ctx context.Context, moduleID string) error {
	if moduleID == "" {
		return nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	filtered := b.subs[:0]
	for _, s := range b.subs {
		if s.moduleID != moduleID {
			filtered = append(filtered, s)
		}
	}
	b.subs = filtered
	return nil
}

func (b *EventBus) Request(ctx context.Context, event contracts.Event, timeout time.Duration) (contracts.Event, error) {
	type result struct {
		event contracts.Event
		err   error
	}
	ch := make(chan result, 1)

	replyHandler := func(ctx context.Context, e contracts.Event) error {
		ch <- result{event: e}
		return nil
	}
	cancel, err := b.Subscribe(ctx, contracts.ReplyEventType(event.Type), replyHandler)
	if err != nil {
		return contracts.Event{}, err
	}
	defer cancel()

	if err := b.Publish(ctx, event); err != nil {
		return contracts.Event{}, err
	}

	select {
	case r := <-ch:
		return r.event, r.err
	case <-ctx.Done():
		return contracts.Event{}, ctx.Err()
	case <-time.After(timeout):
		return contracts.Event{}, context.DeadlineExceeded
	}
}

// PublishedEvents returns all events published since creation or last reset.
func (b *EventBus) PublishedEvents() []contracts.Event {
	b.mu.RLock()
	defer b.mu.RUnlock()
	result := make([]contracts.Event, len(b.Published))
	copy(result, b.Published)
	return result
}

// HandlerCount returns the number of active subscriptions for the given event type.
// Useful for test assertions.
func (b *EventBus) HandlerCount(eventType string) int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	count := 0
	for _, s := range b.subs {
		if s.eventType == eventType {
			count++
		}
	}
	return count
}

// Reset clears all published events, handlers, and errors.
func (b *EventBus) Reset() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.Published = nil
	b.subs = nil
	b.HandlerErrors = nil
}
