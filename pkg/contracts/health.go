package contracts

import "context"

// HealthMonitor runs periodic health checks on all registered modules
// and publishes module.degraded events for unhealthy modules.
// Core starts the HealthMonitor at bootstrap if one is discovered from
// the registry. If no module implements this contract, periodic health
// monitoring is skipped (modules still report via /health endpoint).
type HealthMonitor interface {
	// StartMonitoring begins periodic health checking. The monitor calls Health()
	// on every registered module at the configured interval and publishes
	// events on the bus for any degraded modules.
	StartMonitoring(ctx context.Context, reg Registry, bus EventBus) error

	// Stop gracefully stops the health monitor.
	Stop(ctx context.Context) error
}
