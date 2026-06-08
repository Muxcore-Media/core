package contracts

import (
	"context"
	"time"
)

// Event is the envelope for all inter-module communication.
// Type is a dotted string like "module.registered" or "media.requested".
// Event type constants and payload schemas are defined by contract repos,
// not by core. Core only defines the module-lifecycle event types (below).
// Cluster-lifecycle events are defined in cluster.go.
type Event struct {
	ID        string
	Type      string
	Source    string
	TraceID   string
	Payload   []byte
	Metadata  map[string]string
	Timestamp time.Time
}

// EventHandler is a callback invoked when a matching event is published.
type EventHandler func(ctx context.Context, event Event) error

// EventBus is the core message bus. All module communication flows through it.
type EventBus interface {
	Publish(ctx context.Context, event Event) error
	Subscribe(ctx context.Context, eventType string, handler EventHandler) error
	Unsubscribe(ctx context.Context, eventType string, handler EventHandler) error
	// Request publishes an event and waits for a reply on event.Type + ".reply".
	Request(ctx context.Context, event Event, timeout time.Duration) (Event, error)
}

// Module-lifecycle event types — the only domain event types core knows about.
// Cluster events are in cluster.go. All other event types are in contract repos.
const (
	EventModuleRegistered   = "module.registered"
	EventModuleUnregistered = "module.unregistered"
	EventModuleDegraded     = "module.degraded"
)
