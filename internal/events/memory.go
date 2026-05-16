package events

import (
	"context"
	"fmt"
	"sync"
	"time"

	"encoding/json"

	"github.com/Muxcore-Media/core/internal/trace"
	"github.com/Muxcore-Media/core/pkg/contracts"
	"github.com/google/uuid"
)

type sub struct {
	eventType string
	handler   contracts.EventHandler
}

type MemoryBus struct {
	mu               sync.RWMutex
	subscribers      []sub
	EnableValidation bool             // when true, validates payloads against known schemas
	tracer           contracts.Tracer // optional tracer for span creation
}

func NewMemoryBus() *MemoryBus {
	return &MemoryBus{}
}

// SetTracer configures the tracer for event bus span creation.
// If not set, event operations produce no tracing spans.
func (b *MemoryBus) SetTracer(t contracts.Tracer) {
	b.tracer = t
}

// SetValidation enables or disables payload schema validation on publish.
func (b *MemoryBus) SetValidation(enabled bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.EnableValidation = enabled
}

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

	if b.EnableValidation {
		if err := validateEventPayload(event); err != nil {
			return fmt.Errorf("event validation failed: %w", err)
		}
	}

	if b.tracer != nil {
		var span contracts.Span
		span, ctx = b.tracer.Start(ctx, "event.publish."+event.Type, contracts.SpanKindProducer)
		defer span.End()
		span.SetAttribute("event.id", event.ID)
		span.SetAttribute("event.type", event.Type)
	}

	b.mu.RLock()
	subs := make([]sub, len(b.subscribers))
	copy(subs, b.subscribers)
	b.mu.RUnlock()

	for _, s := range subs {
		if s.eventType == event.Type || s.eventType == "*" {
			go func(h contracts.EventHandler) {
				handlerCtx := context.Background()
				if event.TraceID != "" {
					handlerCtx = trace.WithTraceID(handlerCtx, event.TraceID)
				}
				if b.tracer != nil {
					var span contracts.Span
					span, handlerCtx = b.tracer.Start(handlerCtx, "event.handle."+event.Type, contracts.SpanKindConsumer)
					defer span.End()
					span.SetAttribute("event.id", event.ID)
					span.SetAttribute("event.type", event.Type)
				}
				if err := h(handlerCtx, event); err != nil {
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

func validateEventPayload(event contracts.Event) error {
	switch event.Type {
	case contracts.EventMediaRequested:
		return validatePayload[contracts.MediaRequestedPayload](event.Payload)
	case contracts.EventDownloadStarted:
		return validatePayload[contracts.DownloadStartedPayload](event.Payload)
	case contracts.EventDownloadCompleted:
		return validatePayload[contracts.DownloadCompletedPayload](event.Payload)
	case contracts.EventDownloadFailed:
		return validatePayload[contracts.DownloadFailedPayload](event.Payload)
	case contracts.EventTranscodeStarted:
		return validatePayload[contracts.TranscodeStartedPayload](event.Payload)
	case contracts.EventTranscodeCompleted:
		return validatePayload[contracts.TranscodeCompletedPayload](event.Payload)
	case contracts.EventTranscodeFailed:
		return validatePayload[contracts.TranscodeFailedPayload](event.Payload)
	case contracts.EventLibraryItemAdded:
		return validatePayload[contracts.LibraryItemAddedPayload](event.Payload)
	case contracts.EventLibraryItemRemoved:
		return validatePayload[contracts.LibraryItemRemovedPayload](event.Payload)
	case contracts.EventPlaybackStarted:
		return validatePayload[contracts.PlaybackStartedPayload](event.Payload)
	case contracts.EventPlaybackStopped:
		return validatePayload[contracts.PlaybackStoppedPayload](event.Payload)
	case contracts.EventModuleDegraded:
		return validatePayload[contracts.ModuleDegradedPayload](event.Payload)
	case contracts.EventContentMissing:
		return validatePayload[contracts.ContentMissingPayload](event.Payload)
	case contracts.EventContentFetched:
		return validatePayload[contracts.ContentFetchedPayload](event.Payload)
	case contracts.EventModuleRegistered:
		return validatePayload[contracts.ModuleRegisteredPayload](event.Payload)
	case contracts.EventModuleUnregistered:
		return validatePayload[contracts.ModuleUnregisteredPayload](event.Payload)
	case contracts.EventClusterNodeJoined:
		return validatePayload[contracts.NodeJoinedPayload](event.Payload)
	case contracts.EventClusterNodeLeft:
		return validatePayload[contracts.NodeLeftPayload](event.Payload)
	case contracts.EventClusterLeaderChanged:
		return validatePayload[contracts.LeaderChangedPayload](event.Payload)
	case contracts.EventQualityDecision:
		return validatePayload[contracts.QualityDecisionPayload](event.Payload)
	case contracts.EventFormatMatched:
		return validatePayload[contracts.FormatMatchedPayload](event.Payload)
	case contracts.EventMediaAnalyzed:
		return validatePayload[contracts.MediaAnalyzedPayload](event.Payload)
	default:
		return nil // unknown event types pass through
	}
}

func validatePayload[T any](payload []byte) error {
	var v T
	if err := json.Unmarshal(payload, &v); err != nil {
		return fmt.Errorf("invalid payload for %T: %w", v, err)
	}
	return nil
}
