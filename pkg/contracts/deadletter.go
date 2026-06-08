package contracts

import (
	"context"
	"time"
)

// DeadLetterEntry stores a failed event with its error context.
type DeadLetterEntry struct {
	Event       Event
	HandlerName string
	Error       string
	FailedAt    time.Time
	RetryCount  int
}

// DeadLetterProvider stores events that failed to process so they can be
// inspected, replayed, or discarded. Modules that handle critical events
// (workflow steps, downloads, imports) can configure a dead letter provider
// to prevent silent data loss when a handler transiently fails.
//
// DeadLetterProvider is discovered via FindByCapability("deadletter").
// If no module implements it, failed events are logged and dropped.
type DeadLetterProvider interface {
	// Store records a failed event delivery. Called by the event bus
	// or by modules that want explicit dead-letter handling.
	Store(ctx context.Context, event Event, handlerName string, err error) error

	// Replay returns failed events for the given handler since the given time.
	// Sorted by FailedAt ascending.
	Replay(ctx context.Context, handlerName string, since time.Time) ([]DeadLetterEntry, error)

	// Discard removes a failed event from the dead letter store after
	// the issue has been resolved.
	Discard(ctx context.Context, eventID string) error
}
