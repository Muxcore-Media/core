package contracts

// Module-lifecycle event payload schemas.
// Cluster event payloads are in cluster.go.
// Domain event payloads are in their respective contract repos.

// ModuleRegisteredPayload is the payload for module.registered events.
type ModuleRegisteredPayload struct {
	ModuleID string `json:"module_id"`
	Version  string `json:"version"`
}

// ModuleUnregisteredPayload is the payload for module.unregistered events.
type ModuleUnregisteredPayload struct {
	ModuleID string `json:"module_id"`
}

// ModuleDegradedPayload is the payload for module.degraded events.
type ModuleDegradedPayload struct {
	ModuleID string `json:"module_id"`
	Error    string `json:"error"`
}

// EventSchemaVersion is the current schema version for all event payloads.
// RESERVED: defined for future typed-payload support. Currently the event bus
// uses raw []byte Payload and this constant is not consumed at runtime.
const EventSchemaVersion = "v1"
