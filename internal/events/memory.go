package events

import (
	"context"
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"log/slog"

	"github.com/Muxcore-Media/core/internal/trace"
	"github.com/Muxcore-Media/core/pkg/contracts"
	"github.com/google/uuid"
)

// subscriberBufferSize is the default per-subscriber channel buffer.
// When full, events are dropped for that subscriber.
const subscriberBufferSize = 256

// defaultHandlerTimeout is the deadline for each handler invocation.
const defaultHandlerTimeout = 30 * time.Second

// globalPublishTimeout caps the total time Publish spends dispatching to
// all subscribers. After this deadline, remaining goroutines are abandoned.
const globalPublishTimeout = 60 * time.Second

type sub struct {
	eventType string
	handler   contracts.EventHandler
	moduleID  string // optional module owner for UnsubscribeAll

	// Backpressure: per-subscriber event channel.
	// Written to by Publish, drained by the subscriber worker.
	ch chan contracts.Event

	// ctx/cancel for the subscriber worker goroutine lifecycle.
	ctx    context.Context
	cancel context.CancelFunc

	// dropped counts events dropped due to full channel.
	dropped atomic.Int64
	// processed counts events successfully delivered and handled.
	processed atomic.Int64
	// totalLatencyNs accumulates handler execution time in nanoseconds.
	totalLatencyNs atomic.Int64
}

// MemoryBus is the default in-memory event bus. It dispatches events
// to matching subscribers via bounded per-subscriber channels, providing
// backpressure protection: slow subscribers have events dropped rather
// than blocking the publisher or consuming unbounded memory.
type MemoryBus struct {
	mu            sync.RWMutex
	subscribers   []*sub
	sem           chan struct{}
	audit         contracts.AuditLogger
	nodeID        string
	publishPolicy contracts.PublishPolicyProvider
	// wal is an optional write-ahead log for event persistence and replay.
	// Set via EnableWAL().
	wal *WALWriter
	// moduleTimeouts maps moduleID → per-handler timeout override.
	moduleTimeouts map[string]time.Duration
}

// NewMemoryBus creates an in-memory event bus.
func NewMemoryBus() *MemoryBus {
	return &MemoryBus{
		sem:            make(chan struct{}, runtime.NumCPU()*2),
		moduleTimeouts: make(map[string]time.Duration),
	}
}

// SetAuditLogger configures an audit logger for event bus operations.
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

// SetPublishPolicy configures a publish policy provider consulted before
// every Publish() call. When nil (default), all publishes are allowed.
func (b *MemoryBus) SetPublishPolicy(p contracts.PublishPolicyProvider) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.publishPolicy = p
}

// SetSubscriberTimeout sets a per-module handler timeout override.
// Modules with tight latency requirements can request shorter timeouts.
// The timeout applies to handlers registered via SubscribeModule.
func (b *MemoryBus) SetSubscriberTimeout(moduleID string, timeout time.Duration) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.moduleTimeouts[moduleID] = timeout
}

// Publish dispatches an event to all matching subscribers.
// Each subscriber has a bounded channel; if the channel is full, the event
// is dropped for that subscriber and a counter is incremented.
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
	if publishPolicy == nil {
		return fmt.Errorf("publish denied: no publish policy configured — event %q cannot be dispatched", event.Type)
	}
	callerID := contracts.CallerIDFromContext(ctx)
	if rpp, ok := publishPolicy.(contracts.ResourcePublishPolicyProvider); ok {
		allowed, err := rpp.CanPublishEvent(ctx, callerID, event)
		if err != nil {
			return fmt.Errorf("publish policy error for event %q: %w", event.Type, err)
		}
		if !allowed {
			return fmt.Errorf("publish denied: caller %q not authorized to emit %q events", callerID, event.Type)
		}
	} else {
		allowed, err := publishPolicy.CanPublish(ctx, callerID, event.Type)
		if err != nil {
			return fmt.Errorf("publish policy error for event %q: %w", event.Type, err)
		}
		if !allowed {
			return fmt.Errorf("publish denied: caller %q not authorized to emit %q events", callerID, event.Type)
		}
	}

	b.mu.RLock()
	subs := make([]*sub, 0, len(b.subscribers))
	for _, s := range b.subscribers {
		if s.eventType == event.Type || s.eventType == "*" {
			subs = append(subs, s)
		}
	}
	auditLogger := b.audit
	nodeID := b.nodeID
	b.mu.RUnlock()

	// Audit the event publication.
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
			if err := auditLogger.Log(ctx, entry); err != nil {
				slog.Error("audit log write failed", "event_type", event.Type, "error", err)
			}
		}()
	}

	// Dispatch to each subscriber via its bounded channel.
	// Non-blocking send: if the channel is full, drop and count.
	publishCtx, publishCancel := context.WithTimeout(ctx, globalPublishTimeout)
	defer publishCancel()

	for _, s := range subs {
		select {
		case s.ch <- event:
			// Delivered to subscriber channel.
		case <-publishCtx.Done():
			// Global publish deadline exceeded — stop dispatching.
			slog.Warn("event publish deadline exceeded, dropping remaining subscribers",
				"event_type", event.Type,
				"event_id", event.ID,
			)
			goto done
		default:
			// Channel full — drop event for this subscriber.
			s.dropped.Add(1)
			slog.Warn("subscriber channel full, dropping event",
				"event_type", event.Type,
				"event_id", event.ID,
				"module_id", s.moduleID,
				"dropped_total", s.dropped.Load(),
			)
		}
	}
done:
	return nil
}

// Subscribe registers a handler for the given event type.
// Use "*" to subscribe to all events.
// The handler is invoked by a dedicated goroutine that drains events from
// a bounded channel. Slow handlers have events dropped rather than blocking
// publishers.
func (b *MemoryBus) Subscribe(ctx context.Context, eventType string, handler contracts.EventHandler) error {
	return b.subscribeInternal(ctx, "", eventType, handler, defaultHandlerTimeout)
}

// SubscribeModule registers a handler tagged with a module identifier.
func (b *MemoryBus) SubscribeModule(ctx context.Context, moduleID, eventType string, handler contracts.EventHandler) error {
	b.mu.RLock()
	timeout := defaultHandlerTimeout
	if t, ok := b.moduleTimeouts[moduleID]; ok {
		timeout = t
	}
	b.mu.RUnlock()
	return b.subscribeInternal(ctx, moduleID, eventType, handler, timeout)
}

func (b *MemoryBus) subscribeInternal(_ context.Context, moduleID, eventType string, handler contracts.EventHandler, timeout time.Duration) error {
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
			if err := auditLogger.Log(context.Background(), entry); err != nil {
				slog.Error("audit log write failed", "event_type", eventType, "error", err)
			}
		}()
	}

	workerCtx, workerCancel := context.WithCancel(context.Background())
	s := &sub{
		eventType: eventType,
		handler:   handler,
		moduleID:  moduleID,
		ch:        make(chan contracts.Event, subscriberBufferSize),
		ctx:       workerCtx,
		cancel:    workerCancel,
	}

	// Start the dedicated worker goroutine for this subscriber.
	go b.subscriberWorker(s, timeout)

	b.subscribers = append(b.subscribers, s)
	return nil
}

// subscriberWorker drains the subscriber's channel and invokes the handler.
// Each handler invocation gets a timeout context. When the subscriber is
// removed (Unsubscribe/UnsubscribeAll), the worker's context is cancelled.
func (b *MemoryBus) subscriberWorker(s *sub, timeout time.Duration) {
	for {
		select {
		case <-s.ctx.Done():
			return
		case event, ok := <-s.ch:
			if !ok {
				return
			}
			// Acquire semaphore slot for bounded concurrency.
			select {
			case b.sem <- struct{}{}:
			case <-s.ctx.Done():
				return
			}

			start := time.Now()

			handlerCtx, cancel := context.WithTimeout(s.ctx, timeout)
			if event.TraceID != "" {
				handlerCtx = trace.WithTraceID(handlerCtx, event.TraceID)
			}

			err := s.handler(handlerCtx, event)
			cancel()

			<-b.sem // release semaphore

			s.processed.Add(1)
			s.totalLatencyNs.Add(int64(time.Since(start)))

			if err != nil {
				slog.Error("event handler error",
					"type", event.Type,
					"id", event.ID,
					"module_id", s.moduleID,
					"error", err,
				)
			}
		}
	}
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
			if err := auditLogger.Log(context.Background(), entry); err != nil {
				slog.Error("audit log write failed", "event_type", eventType, "error", err)
			}
		}()
	}

	ptr := fmt.Sprintf("%p", handler)
	filtered := b.subscribers[:0]
	for _, s := range b.subscribers {
		if s.eventType == eventType && fmt.Sprintf("%p", s.handler) == ptr {
			// Cancel the worker goroutine.
			s.cancel()
			continue
		}
		filtered = append(filtered, s)
	}
	b.subscribers = filtered
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
			s.cancel()
			continue
		}
		filtered = append(filtered, s)
	}
	b.subscribers = filtered
	return nil
}

// --- Stats ---

// SubscriptionStat describes subscriptions for a given event type.
type SubscriptionStat struct {
	EventType       string `json:"event_type"`
	SubscriberCount int    `json:"subscriber_count"`
}

// SubscriberCount returns the total number of active subscriber channels.
// A healthy bus always returns >= 0. Used by health probes.
func (b *MemoryBus) SubscriberCount() int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return len(b.subscribers)
}

// SubscriptionStats returns a summary of active subscriptions grouped by
// event type. Used by the admin API for runtime introspection.
func (b *MemoryBus) SubscriptionStats() []SubscriptionStat {
	b.mu.RLock()
	defer b.mu.RUnlock()

	counts := make(map[string]int)
	for _, s := range b.subscribers {
		counts[s.eventType]++
	}

	stats := make([]SubscriptionStat, 0, len(counts))
	for eventType, count := range counts {
		stats = append(stats, SubscriptionStat{
			EventType:       eventType,
			SubscriberCount: count,
		})
	}
	return stats
}

// SubscriberStat provides per-subscriber backpressure and latency metrics.
type SubscriberStat struct {
	EventType     string `json:"event_type"`
	ModuleID      string `json:"module_id,omitempty"`
	Processed     int64  `json:"processed"`
	Dropped       int64  `json:"dropped"`
	AvgLatencyUs  int64  `json:"avg_latency_us"`
}

// SubscriberStats returns per-subscriber metrics for monitoring.
func (b *MemoryBus) SubscriberStats() []SubscriberStat {
	b.mu.RLock()
	defer b.mu.RUnlock()

	stats := make([]SubscriberStat, 0, len(b.subscribers))
	for _, s := range b.subscribers {
		processed := s.processed.Load()
		totalLat := s.totalLatencyNs.Load()
		avgUs := int64(0)
		if processed > 0 {
			avgUs = (totalLat / processed) / 1000
		}
		stats = append(stats, SubscriberStat{
			EventType:    s.eventType,
			ModuleID:     s.moduleID,
			Processed:    processed,
			Dropped:      s.dropped.Load(),
			AvgLatencyUs: avgUs,
		})
	}
	return stats
}

// DroppedEvents returns the total number of events dropped across all
// subscribers due to full channels. Key metric for backpressure monitoring.
func (b *MemoryBus) DroppedEvents() int64 {
	b.mu.RLock()
	defer b.mu.RUnlock()

	var total int64
	for _, s := range b.subscribers {
		total += s.dropped.Load()
	}
	return total
}

// --- Request/reply ---

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
