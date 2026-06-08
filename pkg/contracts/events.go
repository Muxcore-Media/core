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

// PublishPolicyProvider determines whether a caller is authorized to publish
// events of a given type. Implemented by modules that define event-level
// access control. The event bus consults this before dispatching.
//
// SECURITY: When no PublishPolicyProvider is registered, the event bus MUST
// deny all event publication. This is a change from the previous behavior
// where the default was open mode. Deployments that want open mode must
// register an explicit permissive policy provider.
//
// For resource-level event authorization, implement the optional
// ResourcePublishPolicyProvider interface. The event bus will type-assert
// and use the richer interface when available.
type PublishPolicyProvider interface {
	// CanPublish returns (true, nil) if the caller is permitted to publish
	// events of the given type. Returns (false, nil) if publishing is denied.
	CanPublish(ctx context.Context, callerID, eventType string) (bool, error)
}

// ResourcePublishPolicyProvider is an optional extension that enables
// resource-level event authorization with access to the full event payload.
// Implement this alongside PublishPolicyProvider to enable checks like
// "can this caller publish media.updated events for media item X?".
//
// When a provider implements this interface, the event bus calls
// CanPublishEvent before CanPublish, giving the provider full event context.
type ResourcePublishPolicyProvider interface {
	PublishPolicyProvider

	// CanPublishEvent performs event-level authorization with access to
	// the full event payload. This enables resource-level checks beyond
	// the type-level check in CanPublish.
	CanPublishEvent(ctx context.Context, callerID string, event Event) (bool, error)
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

	// SubscribeModule registers a handler tagged with a module identifier.
	// This enables UnsubscribeAll for clean module lifecycle management —
	// when a module shuts down, all its subscriptions are removed at once.
	SubscribeModule(ctx context.Context, moduleID, eventType string, handler EventHandler) error

	// UnsubscribeAll removes all subscriptions tagged with the given module ID.
	// If moduleID is empty, this is a no-op. Used by module lifecycle management
	// during shutdown to prevent handlers from receiving events after the
	// module has stopped.
	UnsubscribeAll(ctx context.Context, moduleID string) error
}

// Module-lifecycle event types — the only domain event types core knows about.
// Cluster events are in cluster.go. All other event types are in contract repos.
const (
	EventModuleRegistered   = "module.registered"
	EventModuleUnregistered = "module.unregistered"
	EventModuleDegraded     = "module.degraded"
)
