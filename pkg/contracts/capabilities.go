package contracts

// Capability strings for all discoverable services.
// Use these constants with Registry.FindByCapability and in ModuleInfo.Capabilities
// to avoid silent typo failures.
const (
	CapabilitySecrets         = "secrets"
	CapabilityDatabase        = "database"
	CapabilityCache           = "cache"
	CapabilityCacheLocal      = "cache.local"
	CapabilityMetrics         = "metrics"
	CapabilityTracing         = "tracing"
	CapabilityCircuitBreaker  = "circuitbreaker"
	CapabilityConfigWatcher   = "config.watcher"
	CapabilityDeadLetter      = "deadletter"
	CapabilityCallPolicy      = "call.policy"
	CapabilityPublishPolicy   = "publish.policy"
	CapabilityIdentity        = "identity"
	CapabilityLogging         = "logging"
	CapabilityRetry           = "retry"
	CapabilityIdempotency     = "idempotency"
	CapabilityFeatureFlags    = "feature.flags"
	CapabilitySerialization   = "serialization"
	CapabilityEncryption      = "encryption"
	CapabilityDistributedLock = "distributed.lock"
	CapabilityDataRedaction   = "data.redaction"
	CapabilityEventStore      = "event.store"
	CapabilityInputValidate   = "input.validate"
	CapabilityWorkflowEngine  = "workflow.engine"
	CapabilitySpoolResolver   = "spool.resolver"
	CapabilityScheduler       = "scheduler"
	CapabilityHealthMonitor   = "health.monitor"
	CapabilityBackup          = "backup"

	// Worker pool capabilities.
	CapabilityWorkerPool = "worker.pool"
	// CapabilityExecutorPrefix is the prefix for executor discovery.
	// Modules handling task type "download" advertise "executor.download".
	CapabilityExecutorPrefix = "executor."

	// Auth/security capabilities.
	CapabilityAuth        = "auth"
	CapabilityAuthorizer  = "authorizer"
	CapabilityRateLimiter = "ratelimit"
)
