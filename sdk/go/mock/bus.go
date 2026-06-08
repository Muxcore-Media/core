package mock

import (
	"context"
	"sync"
	"time"

	"github.com/Muxcore-Media/core/pkg/contracts"
)

// EventBus is a mock event bus for module testing.
type EventBus struct {
	mu        sync.RWMutex
	Published []contracts.Event
	Handlers   map[string][]contracts.EventHandler
	moduleOwned map[string][]int // moduleID -> handler indices into a global ref list
	handlerRefs []contracts.EventHandler
}

func NewEventBus() *EventBus {
	return &EventBus{
		Published: make([]contracts.Event, 0),
		Handlers:    make(map[string][]contracts.EventHandler),
		moduleOwned: make(map[string][]int),
		handlerRefs: make([]contracts.EventHandler, 0),
	}
}

func (b *EventBus) Publish(ctx context.Context, event contracts.Event) error {
	b.mu.Lock()
	b.Published = append(b.Published, event)
	handlers := b.Handlers[event.Type]
	b.mu.Unlock()

	for _, h := range handlers {
		_ = h(ctx, event)
	}
	return nil
}

func (b *EventBus) Subscribe(ctx context.Context, eventType string, handler contracts.EventHandler) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.Handlers[eventType] = append(b.Handlers[eventType], handler)
	return nil
}

func (b *EventBus) Unsubscribe(ctx context.Context, eventType string, handler contracts.EventHandler) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.Handlers, eventType)
	return nil
}

func (b *EventBus) SubscribeModule(ctx context.Context, moduleID, eventType string, handler contracts.EventHandler) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	idx := len(b.handlerRefs)
	b.handlerRefs = append(b.handlerRefs, handler)
	b.Handlers[eventType] = append(b.Handlers[eventType], handler)
	b.moduleOwned[moduleID] = append(b.moduleOwned[moduleID], idx)
	return nil
}

func (b *EventBus) UnsubscribeAll(ctx context.Context, moduleID string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if moduleID ==  {
		return nil
	}
	indices := b.moduleOwned[moduleID]
	if len(indices) == 0 {
		return nil
	}
	// Collect handlers to remove
	toRemove := make(map[contracts.EventHandler]bool)
	for _, idx := range indices {
		if idx < len(b.handlerRefs) {
			toRemove[b.handlerRefs[idx]] = true
		}
	}
	// Filter all handler maps
	for eventType, handlers := range b.Handlers {
		remaining := handlers[:0]
		for _, h := range handlers {
			if !toRemove[h] {
				remaining = append(remaining, h)
			}
		}
		b.Handlers[eventType] = remaining
	}
	delete(b.moduleOwned, moduleID)
	return nil
}

func (b *EventBus) Request(ctx context.Context, event contracts.Event, timeout time.Duration) (contracts.Event, error) {
	type result struct {
		event contracts.Event
		err   error
	}
	ch := make(chan result, 1)
	replyType := event.Type + ".reply"

	replyHandler := func(ctx context.Context, e contracts.Event) error {
		ch <- result{event: e}
		return nil
	}
	_ = b.Subscribe(ctx, replyType, replyHandler)
	defer b.Unsubscribe(ctx, replyType, replyHandler)

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

// Reset clears all published events and handlers.
func (b *EventBus) Reset() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.Published = nil
	b.Handlers = make(map[string][]contracts.EventHandler)
	b.moduleOwned = make(map[string][]int)
	b.handlerRefs = nil
}
