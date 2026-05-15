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
	Handlers  map[string][]contracts.EventHandler
}

func NewEventBus() *EventBus {
	return &EventBus{
		Published: make([]contracts.Event, 0),
		Handlers:  make(map[string][]contracts.EventHandler),
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

func (b *EventBus) Request(ctx context.Context, event contracts.Event, timeout time.Duration) (contracts.Event, error) {
	_ = b.Publish(ctx, event)
	return contracts.Event{}, nil
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
}
