package events

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/Muxcore-Media/core/pkg/contracts"
	"github.com/google/uuid"
)

type sub struct {
	eventType string
	handler   contracts.EventHandler
}

type MemoryBus struct {
	mu          sync.RWMutex
	subscribers []sub
}

func NewMemoryBus() *MemoryBus {
	return &MemoryBus{}
}

func (b *MemoryBus) Publish(ctx context.Context, event contracts.Event) error {
	if event.ID == "" {
		event.ID = uuid.New().String()
	}
	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now()
	}

	b.mu.RLock()
	subs := make([]sub, len(b.subscribers))
	copy(subs, b.subscribers)
	b.mu.RUnlock()

	for _, s := range subs {
		if s.eventType == event.Type || s.eventType == "*" {
			go func(h contracts.EventHandler) {
				if err := h(ctx, event); err != nil {
					// Log would go here — no silent drops in production
					_ = err
				}
			}(s.handler)
		}
	}
	return nil
}

func (b *MemoryBus) Subscribe(ctx context.Context, eventType string, handler contracts.EventHandler) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.subscribers = append(b.subscribers, sub{
		eventType: eventType,
		handler:   handler,
	})
	return nil
}

func (b *MemoryBus) Unsubscribe(ctx context.Context, eventType string, handler contracts.EventHandler) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	ptr := fmt.Sprintf("%p", handler)
	filtered := b.subscribers[:0]
	for _, s := range b.subscribers {
		if s.eventType == eventType && fmt.Sprintf("%p", s.handler) == ptr {
			continue
		}
		filtered = append(filtered, s)
	}
	b.subscribers = filtered
	return nil
}

func (b *MemoryBus) Request(ctx context.Context, event contracts.Event, timeout time.Duration) (contracts.Event, error) {
	if event.ID == "" {
		event.ID = uuid.New().String()
	}
	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now()
	}

	type result struct {
		event contracts.Event
		err   error
	}

	ch := make(chan result, 1)
	replyType := event.Type + ".reply"

	subErr := b.Subscribe(ctx, replyType, func(ctx context.Context, e contracts.Event) error {
		ch <- result{event: e}
		return nil
	})
	if subErr != nil {
		return contracts.Event{}, subErr
	}
	defer b.Unsubscribe(ctx, replyType, nil)

	if err := b.Publish(ctx, event); err != nil {
		return contracts.Event{}, err
	}

	select {
	case r := <-ch:
		return r.event, r.err
	case <-ctx.Done():
		return contracts.Event{}, ctx.Err()
	case <-time.After(timeout):
		return contracts.Event{}, fmt.Errorf("request timed out after %s", timeout)
	}
}
