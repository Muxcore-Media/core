package events

import (
	"context"
	"fmt"
	"log/slog"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Muxcore-Media/core/internal/callerid"
	"github.com/Muxcore-Media/core/internal/trace"
	"github.com/Muxcore-Media/core/pkg/contracts"
	"github.com/google/uuid"
)

// subscriberBufferSize is the default per-subscriber channel buffer.
// When full, events are dropped for that subscriber.
const subscriberBufferSize = 256

// maxSubscribers is the maximum number of concurrent subscriber goroutines.
// Prevents unbounded goroutine and channel creation (DoS vector).
// Each subscriber creates 1 goroutine + 1 channel with subscriberBufferSize
// capacity. 10000 subscribers at 256 slots each = 2.56M events in flight max.
const maxSubscribers = 10000

// defaultHandlerTimeout is the deadline for each handler invocation.
const defaultHandlerTimeout = 30 * time.Second

// globalPublishTimeout caps the total time Publish spends dispatching to
// all subscribers. After this deadline, remaining goroutines are abandoned.
const globalPublishTimeout = 60 * time.Second

type sub struct { //nolint:govet // struct field alignment is acceptable
	eventType string
	handler   contracts.EventHandler
	moduleID  string // optional module owner for UnsubscribeAll

	// Backpressure: per-subscriber event channel.
	// Written to by Publish, drained by the subscriber worker.
	ch chan contracts.Event

	// cancel for the subscriber worker goroutine lifecycle.
	cancel context.CancelFunc
	// done is closed when the subscriber worker exits.
	done chan struct{}

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
//
// Call Close() during shutdown to cancel all subscriber worker goroutines.
type MemoryBus struct { //nolint:govet // struct field alignment is acceptable
	mu            sync.RWMutex
	subscribers   []*sub
	sem           chan struct{}
	audit         contracts.AuditLogger
	nodeID        string
	publishPolicy contracts.PublishPolicyProvider
	// wal is an optional write-ahead log for event persistence and replay.
	// Set via EnableWAL().
	wal *WALWriter
	// closed signals that the bus has been shut down.
	closed chan struct{}
	// publishCount counts total successful publishes for metrics.
	publishCount atomic.Int64
	// WALReplayTimeout is the per-subscriber timeout for WAL replay during
	// SubscribeFrom. Zero or negative uses the default of 30s.
	WALReplayTimeout time.Duration
}

// NewMemoryBus creates an in-memory event bus.
func NewMemoryBus() *MemoryBus {
	return &MemoryBus{
		sem:    make(chan struct{}, runtime.NumCPU()*2),
		closed: make(chan struct{}),
	}
}

// Close cancels all subscriber worker goroutines, drains remaining events,
// closes the WAL, and prevents new subscriptions.
// Safe to call multiple times. After Close, Subscribe/SubscribeModule return an error.
// Outstanding cancel functions become no-ops after Close.
func (b *MemoryBus) Close() {
	b.mu.Lock()
	select {
	case <-b.closed:
		b.mu.Unlock()
		return
	default:
		close(b.closed)
	}
	subs := make([]*sub, len(b.subscribers))
	copy(subs, b.subscribers)
	b.subscribers = nil
	wal := b.wal
	b.wal = nil
	b.mu.Unlock()

	// Stop workers first so only Close drains remaining channel events
	// (avoids racing the worker's channel receive / dropping dequeued events).
	for _, s := range subs {
		s.cancel()
	}
	for _, s := range subs {
		<-s.done
	}

	drainCtx, drainCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer drainCancel()
	var wg sync.WaitGroup
	for _, s := range subs {
		wg.Add(1)
		go func(s *sub) {
			defer func() {
				if r := recover(); r != nil {
					slog.Error("event bus drain handler panic recovered", "panic", r)
				}
				wg.Done()
			}()
			for {
				select {
				case <-drainCtx.Done():
					return
				case event, ok := <-s.ch:
					if !ok {
						return
					}
					handlerCtx, cancel := context.WithTimeout(drainCtx, 2*time.Second)
					err := s.handler(handlerCtx, event)
					cancel()
					if err != nil {
						slog.Error("event bus drain: handler error",
							"type", event.Type, "error", err)
					}
				default:
					return
				}
			}
		}(s)
	}
	wg.Wait()

	// Close the WAL to flush buffered writes.
	if wal != nil {
		if err := wal.Close(); err != nil {
			slog.Error("event bus: close WAL", "error", err)
		}
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
// every Publish() call. When nil, all publishes are denied (deny-by-default).
func (b *MemoryBus) SetPublishPolicy(p contracts.PublishPolicyProvider) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.publishPolicy = p
}

func (b *MemoryBus) PublishPolicy() contracts.PublishPolicyProvider {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.publishPolicy
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
		return fmt.Errorf("publish denied: no publish policy configured — event %q cannot be dispatched. Deploy a module implementing publish.policy", event.Type)
	}
	callerID := callerid.Get(ctx)
	allowed, err := checkPublishAllowed(ctx, callerID, event, publishPolicy)
	if err != nil {
		return fmt.Errorf("publish policy error for event %q: %w", event.Type, err)
	}
	if !allowed {
		return fmt.Errorf("publish denied: caller %q not authorized to emit %q events", callerID, event.Type)
	}

	b.publishCount.Add(1)

	b.mu.RLock()
	subs := make([]*sub, 0, len(b.subscribers))
	for _, s := range b.subscribers {
		if s.eventType == event.Type || s.eventType == "*" {
			subs = append(subs, s)
		}
	}
	auditLogger := b.audit
	nodeID := b.nodeID
	wal := b.wal
	b.mu.RUnlock()

	// Write to WAL before dispatching, so replay gets persisted events.
	if wal != nil {
		if _, err := wal.Write(event); err != nil {
			slog.Error("WAL write failed, event may not be recoverable",
				"event_type", event.Type, "event_id", event.ID, "error", err)
		}
	}

	// Audit the event publication.
	if auditLogger != nil {
		go func() {
			defer func() {
				if r := recover(); r != nil {
					slog.Error("audit publish panic recovered", "event_type", event.Type, "panic", r)
				}
			}()
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
				NodeID:  nodeID,
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
		case <-publishCtx.Done():
			slog.Warn("event publish deadline exceeded, dropping remaining subscribers",
				"event_type", event.Type,
				"event_id", event.ID,
			)
			goto done
		default:
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
// publishers. Returns a cancel function that removes the subscription.
func (b *MemoryBus) Subscribe(ctx context.Context, eventType string, handler contracts.EventHandler) (func(), error) {
	return b.subscribeInternal(ctx, "", eventType, handler, defaultHandlerTimeout)
}

// SubscribeModule registers a handler tagged with a module identifier.
// Returns a cancel function that removes this subscription.
func (b *MemoryBus) SubscribeModule(ctx context.Context, moduleID, eventType string, handler contracts.EventHandler) (func(), error) {
	return b.subscribeInternal(ctx, moduleID, eventType, handler, defaultHandlerTimeout)
}

func (b *MemoryBus) subscribeInternal(_ context.Context, moduleID, eventType string, handler contracts.EventHandler, timeout time.Duration) (func(), error) {
	b.mu.Lock()

	select {
	case <-b.closed:
		b.mu.Unlock()
		return func() {}, fmt.Errorf("event bus is closed")
	default:
	}

	// Enforce subscriber cap to prevent unbounded goroutine and channel
	// creation (DoS via Subscribe flooding).
	if len(b.subscribers) >= maxSubscribers {
		b.mu.Unlock()
		return func() {}, fmt.Errorf("event bus: maximum subscribers (%d) reached", maxSubscribers)
	}

	// Audit the subscription if configured.
	auditLogger := b.audit
	if auditLogger != nil {
		go func() { //nolint:gosec,contextcheck // fire-and-forget audit — no request context to propagate
			defer func() {
				if r := recover(); r != nil {
					slog.Error("audit subscribe panic recovered", "event_type", eventType, "panic", r)
				}
			}()
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
		cancel:    workerCancel,
		done:      make(chan struct{}),
	}

	// Start the dedicated worker goroutine for this subscriber.
	go b.subscriberWorker(workerCtx, s, timeout) //nolint:contextcheck // subscriber worker uses its own lifecycle, not caller's context

	b.subscribers = append(b.subscribers, s)
	b.mu.Unlock()

	cancel := func() {
		b.mu.Lock()
		filtered := b.subscribers[:0]
		for _, existing := range b.subscribers {
			if existing == s {
				existing.cancel()
				continue
			}
			filtered = append(filtered, existing)
		}
		b.subscribers = filtered
		b.mu.Unlock()
	}
	return cancel, nil
}

// subscriberWorker drains the subscriber's channel and invokes the handler.
// Each handler invocation gets a timeout context. When the subscriber is
// removed (Unsubscribe/UnsubscribeAll), the worker's context is cancelled.
func (b *MemoryBus) subscriberWorker(ctx context.Context, s *sub, timeout time.Duration) {
	defer close(s.done)
	defer func() {
		if r := recover(); r != nil {
			slog.Error("subscriber worker panic recovered",
				"module_id", s.moduleID,
				"panic", r,
			)
		}
	}()
	for {
		select {
		case <-ctx.Done():
			return
		case event, ok := <-s.ch:
			if !ok {
				return
			}
			// Acquire semaphore slot for bounded concurrency.
			select {
			case b.sem <- struct{}{}:
			case <-ctx.Done():
				// Event already dequeued — leave it for Close drain (do not drop).
				select {
				case s.ch <- event:
				default:
					// Channel full: deliver inline so the event is not lost.
					b.deliverEvent(context.WithoutCancel(ctx), s, event, timeout)
				}
				return
			}

			start := time.Now()

			func() {
				defer func() {
					if r := recover(); r != nil {
						slog.Error("event handler panic recovered",
							"type", event.Type,
							"id", event.ID,
							"module_id", s.moduleID,
							"panic", r,
						)
					}
					<-b.sem // release semaphore on panic or normal return
				}()
				handlerCtx, cancel := context.WithTimeout(ctx, timeout)
				defer cancel()
				if event.TraceID != "" {
					handlerCtx = trace.WithTraceID(handlerCtx, event.TraceID)
				}

				err := s.handler(handlerCtx, event)

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
			}()
		}
	}
}

func (b *MemoryBus) deliverEvent(ctx context.Context, s *sub, event contracts.Event, timeout time.Duration) {
	start := time.Now()
	handlerCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	if event.TraceID != "" {
		handlerCtx = trace.WithTraceID(handlerCtx, event.TraceID)
	}
	err := s.handler(handlerCtx, event)
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

// UnsubscribeAll removes all subscriptions tagged with the given module ID.
func (b *MemoryBus) UnsubscribeAll(ctx context.Context, moduleID string) error {
	b.mu.Lock()
	defer b.mu.Unlock()

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
	EventType    string `json:"event_type"`
	ModuleID     string `json:"module_id,omitempty"`
	Processed    int64  `json:"processed"`
	Dropped      int64  `json:"dropped"`
	AvgLatencyUs int64  `json:"avg_latency_us"`
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

// PublishCount returns the total number of successful publishes.
func (b *MemoryBus) PublishCount() int64 {
	return b.publishCount.Load()
}

// --- Request/reply ---

// Request publishes an event and waits for a reply on ReplyEventType(event.Type).
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
	replyType := contracts.ReplyEventType(event.Type)

	replyHandler := func(ctx context.Context, e contracts.Event) error {
		ch <- result{event: e}
		return nil
	}
	cancel, subErr := b.Subscribe(ctx, replyType, replyHandler)
	if subErr != nil {
		return contracts.Event{}, subErr
	}
	defer cancel()

	if err := b.Publish(ctx, event); err != nil {
		return contracts.Event{}, err
	}

	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case r := <-ch:
		return r.event, r.err
	case <-ctx.Done():
		// Context cancelled — try to drain a pending reply before giving up
		// to avoid dropping a reply that arrived before the cancellation.
		select {
		case r := <-ch:
			return r.event, r.err
		default:
		}
		return contracts.Event{}, ctx.Err()
	case <-timer.C:
		return contracts.Event{}, fmt.Errorf("request timed out after %s", timeout)
	}
}

// checkPublishAllowed delegates to either ResourcePublishPolicyProvider or
// PublishPolicyProvider depending on which interface the policy implements.
func checkPublishAllowed(ctx context.Context, callerID string, event contracts.Event, policy contracts.PublishPolicyProvider) (bool, error) {
	if rpp, ok := policy.(contracts.ResourcePublishPolicyProvider); ok {
		return rpp.CanPublishEvent(ctx, callerID, event)
	}
	return policy.CanPublish(ctx, callerID, event.Type)
}
