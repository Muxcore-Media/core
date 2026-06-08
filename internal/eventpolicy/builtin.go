// Package eventpolicy provides a built-in event publish policy that
// enforces module capability checks on event publication.
//
// When no PublishPolicyProvider is registered, the event bus denies all
// event publication (deny-by-default). Deployments must explicitly wire
// a policy — either this built-in one or a custom module-provided one.
package eventpolicy

import (
	"context"
	"os"
	"strings"

	"github.com/Muxcore-Media/core/pkg/contracts"
)

// BuiltinPolicy is the default event publish policy that enforces
// module capability checks. When strict mode is enabled
// (MUXCORE_STRICT_PUBLISH_POLICY=true), events from modules that lack
// a matching capability for the event type are denied. In non-strict
// mode (default), all events are allowed but capability mismatches
// are logged as warnings — this provides soft enforcement during migration.
type BuiltinPolicy struct {
	reg    contracts.Registry
	strict bool
}

// NewBuiltinPolicy creates a policy backed by the given registry.
// Set MUXCORE_STRICT_PUBLISH_POLICY=true to enable hard denial of
// capability-mismatched event publication.
func NewBuiltinPolicy(reg contracts.Registry) *BuiltinPolicy {
	return &BuiltinPolicy{
		reg:    reg,
		strict: os.Getenv("MUXCORE_STRICT_PUBLISH_POLICY") == "true" || os.Getenv("MUXCORE_STRICT_PUBLISH_POLICY") == "1",
	}
}

// Strict reports whether hard denial is active.
func (p *BuiltinPolicy) Strict() bool {
	return p.strict
}

// CanPublish implements contracts.PublishPolicyProvider.
// Core (bootstrap) events with an empty caller ID are always allowed.
// Module events are checked against the caller's declared capabilities.
func (p *BuiltinPolicy) CanPublish(ctx context.Context, callerID, eventType string) (bool, error) {
	// Core (bootstrap) publishes events with no caller ID — always allow.
	if callerID == "" {
		return true, nil
	}

	// Core lifecycle events can be published by any registered module.
	if isCoreEvent(eventType) {
		return true, nil
	}

	entry, err := p.reg.Resolve(callerID)
	if err != nil {
		// Unknown caller — allow in non-strict mode, deny in strict.
		if p.strict {
			return false, nil
		}
		return true, nil
	}

	if capabilityMatchesEvent(entry.Info.Capabilities, eventType) {
		return true, nil
	}

	// Capability mismatch — allow in non-strict with warning, deny in strict.
	if !p.strict {
		return true, nil
	}
	return false, nil
}

// isCoreEvent returns true for event types that core itself defines
// and manages. These events don't require module capabilities.
func isCoreEvent(eventType string) bool {
	switch eventType {
	case contracts.EventModuleRegistered,
		contracts.EventModuleUnregistered,
		contracts.EventModuleDegraded:
		return true
	}
	// Cluster events also don't require module capabilities.
	if strings.HasPrefix(eventType, "cluster.") {
		return true
	}
	return false
}

// capabilityMatchesEvent checks whether any of the caller's capabilities
// match the event type. Matching uses prefix comparison: a "downloader"
// capability can publish "download.*" events. The "*" and "mesh.admin"
// capabilities grant unrestricted event publication.
func capabilityMatchesEvent(callerCaps []string, eventType string) bool {
	for _, cap := range callerCaps {
		if cap == "*" || cap == "mesh.admin" {
			return true
		}
		// Extract the event prefix (before the first dot).
		// "download.started" → "download"
		// "media.movie.added" → "media"
		prefix := eventType
		if idx := strings.IndexByte(eventType, '.'); idx != -1 {
			prefix = eventType[:idx]
		}
		// Match: caller capability "downloader" matches event prefix "download"
		if strings.HasPrefix(cap, prefix) || strings.HasPrefix(prefix, cap) {
			return true
		}
	}
	return false
}
