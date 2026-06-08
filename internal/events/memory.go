package events

import (
	"context"
	"fmt"
	"runtime"
	"sync"
	"time"

	"log/slog"

	"github.com/Muxcore-Media/core/internal/trace"
	"github.com/Muxcore-Media/core/pkg/contracts"
	"github.com/google/uuid"
)

type sub struct {
	eventType string
	handler   contracts.EventHandler
	moduleID  string // optional module owner for UnsubscribeAll
}

// MemoryBus is the default in-memory event bus. It dispatches events
// to matching subscribers concurrently with bounded goroutine concurrency.
type MemoryBus struct {
	mu            sync.RWMutex
	subscribers   []sub
	sem           chan struct{}
	audit         contracts.AuditLogger
	nodeID        string
	// publishPolicy is consulted before every Publish() to check whether
	// the caller is authorized to emit events of the given type.
	publishPolicy contracts.PublishPolicyProvider
}

// NewMemoryBus creates an in-memory event bus.
func NewMemoryBus() *MemoryBus {
	return &MemoryBus{
		sem: make(chan struct{}, runtime.NumCPU()*2),
	}
}

// SetAuditLogger configures an audit logger for event bus operations.
// SetPublishPolicy configures a publish policy provider consulted before
// every Publish() call. When nil (default), all publishes are allowed.
func (b *MemoryBus) SetPublishPolicy(p contracts.PublishPolicyProvider) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.publishPolicy = p
}

func (b *MemoryBus) SetAuditLogger(a contracts.AuditLogger) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.audit = a
}

// SetNodeID sets the node identifier for audit entries.
func (b *MemoryBus) SetNodeID(id string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.nodeID = id
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

	// Check publish policy before dispatching.
	b.mu.RLock()
	publishPolicy := b.publishPolicy
	b.mu.RUnlock()
	if publishPolicy != nil {
		callerID := contracts.CallerIDFromContext(ctx)
		allowed, err := publishPolicy.CanPublish(ctx, callerID, event.Type)
		if err != nil {
			return fmt.Errorf("publish policy error for event %q: %w", event.Type, err)
		}
		if !allowed {
			return fmt.Errorf("publish denied: caller %q not authorized to emit %q events", callerID, event.Type)
		}
	}

	b.mu.RLock()
	subs := make([]sub, len(b.subscribers))
	copy(subs, b.subscribers)
	auditLogger := b.audit
	nodeID := b.nodeID
	b.mu.RUnlock()

	// Audit the event publication if an audit logger is configured.
	if auditLogger != nil {
		go func() {
			entry := contracts.AuditEntry{
				ID:         uuid.New().String(),
				Timestamp:  time.Now(),
				Actor:      event.Source,
				Action:     "event.publish",
				Resource:   event.Type,
				ResourceID: event.ID,
				Details: map[string]string{
					"subscriber_count": fmt.Sprintf("%d", len(subs)),
				},
				TraceID: event.TraceID,
				NodeID:   nodeID,
			}
			_ = auditLogger.Log(ctx, entry)
		}()
	}

	for _, s := range subs {
		if s.eventType == event.Type || s.eventType == "*" {
			go func(h contracts.EventHandler) {
				b.sem <- struct{}{}
				defer func() { <-b.sem }()
				handlerCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
				defer cancel()
				if event.TraceID != "" {
					handlerCtx = trace.WithTraceID(handlerCtx, event.TraceID)
				}
				if err := h(handlerCtx, event); err != nil {
					slog.Error("event handler error", "type", event.Type, "id", event.ID, "error", err)
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

	// Audit the subscription if configured.
	auditLogger := b.audit
	if auditLogger != nil {
		go func() {
			entry := contracts.AuditEntry{
				ID:        uuid.New().String(),
				Timestamp: time.Now(),
				Actor:     "system",
				Action:    "event.subscribe",
				Resource:  eventType,
				Details: map[string]string{
					"handler": fmt.Sprintf("%p", handler),
				},
				NodeID: b.nodeID,
			}
			_ = auditLogger.Log(ctx, entry)
		}()
	}

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

	// Audit the unsubscription if configured.
	auditLogger := b.audit
	if auditLogger != nil {
		go func() {
			entry := contracts.AuditEntry{
				ID:        uuid.New().String(),
				Timestamp: time.Now(),
				Actor:     "system",
				Action:    "event.unsubscribe",
				Resource:  eventType,
				Details: map[string]string{
					"handler": fmt.Sprintf("%p", handler),
				},
				NodeID: b.nodeID,
			}
			_ = auditLogger.Log(ctx, entry)
		}()
	}

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

// SubscribeModule registers a handler tagged with a module identifier.
// This enables UnsubscribeAll for clean module lifecycle management.
func (b *MemoryBus) SubscribeModule(ctx context.Context, moduleID, eventType string, handler contracts.EventHandler) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.subscribers = append(b.subscribers, sub{
		eventType: eventType,
		handler:   handler,
		moduleID:  moduleID,
	})
	return nil
}

// UnsubscribeAll removes all subscriptions tagged with the given module ID.
func (b *MemoryBus) UnsubscribeAll(ctx context.Context, moduleID string) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	if moduleID == "" {
		return nil
	}

	filtered := b.subscribers[:0]
	for _, s := range b.subscribers {
		if s.moduleID == moduleID {
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
