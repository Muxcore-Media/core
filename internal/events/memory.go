package events

import (
	"context"
	"fmt"
	"runtime"
	"sync"
	"time"

	"log"

	"github.com/Muxcore-Media/core/internal/trace"
	"github.com/Muxcore-Media/core/pkg/contracts"
	"github.com/google/uuid"
)

type sub struct {
	eventType string
	handler   contracts.EventHandler
}

// MemoryBus is the default in-memory event bus. It dispatches events
// to matching subscribers concurrently with bounded goroutine concurrency.
type MemoryBus struct {
	mu          sync.RWMutex
	subscribers []sub
	sem         chan struct{}
}

// NewMemoryBus creates an in-memory event bus.
func NewMemoryBus() *MemoryBus {
	return &MemoryBus{
		sem: make(chan struct{}, runtime.NumCPU()*2),
	}
}

// Publish dispatches an event to all matching subscribers.
// Subscriber handlers run concurrently in their own goroutines.
func (b *MemoryBus) Publish(ctx context.Context, event contracts.Event) error {
	if event.ID == "" {
		event.ID = uuid.New().String()
	}
	if event.TraceID == "" {
		if tid := trace.FromContext(ctx); tid != "" {
			event.TraceID = tid
		}
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
				b.sem <- struct{}{}
				defer func() { <-b.sem }()
				handlerCtx := ctx
				if event.TraceID != "" {
					handlerCtx = trace.WithTraceID(handlerCtx, event.TraceID)
				}
				if err := h(handlerCtx, event); err != nil {
					log.Printf("event handler error: type=%s id=%s: %v", event.Type, event.ID, err)
				}
			}(s.handler)
		}
	}
	return nil
}

// Subscribe registers a handler for the given event type.
// Use "*" to subscribe to all events.
func (b *MemoryBus) Subscribe(ctx context.Context, eventType string, handler contracts.EventHandler) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.subscribers = append(b.subscribers, sub{
		eventType: eventType,
		handler:   handler,
	})
	return nil
}

// Unsubscribe removes a handler for the given event type.
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

// Request publishes an event and waits for a reply.
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

	replyHandler := func(ctx context.Context, e contracts.Event) error {
		ch <- result{event: e}
		return nil
	}
	subErr := b.Subscribe(ctx, replyType, replyHandler)
	if subErr != nil {
		return contracts.Event{}, subErr
	}
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
		return contracts.Event{}, fmt.Errorf("request timed out after %s", timeout)
	}
}
