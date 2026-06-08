package api

import (
	"fmt"
	"net/http"
	"strings"
)

// MetricsProvider supplies runtime metrics for the /metrics endpoint.
// Each field is a function that returns the current value — called on
// every scrape so the response is always fresh.
type MetricsProvider struct {
	DroppedEvents        func() int64
	ActiveSubscribers    func() int
	ConnPoolSize         func() int
	RegistryModuleCount  func() int
	LeaderTerm           func() uint64
	IsLeader             func() bool
	StorageProviderCount func() int
	ModuleDegradedCount  func() int
	GoroutineCount       func() int
	AllocBytes           func() uint64
}

// MetricsHandler returns an http.HandlerFunc that emits Prometheus-format
// metrics. No external dependency — generates text/plain manually.
//
// Enable via MUXCORE_METRICS_ENABLE=true. The endpoint is registered at /metrics.
func MetricsHandler(p *MetricsProvider) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")

		var sb strings.Builder

		// Helper to write a gauge metric.
		gauge := func(name, help string, value float64, labels ...string) {
			sb.WriteString("# HELP ")
			sb.WriteString(name)
			sb.WriteByte(' ')
			sb.WriteString(help)
			sb.WriteByte('\n')
			sb.WriteString("# TYPE ")
			sb.WriteString(name)
			sb.WriteString(" gauge\n")
			sb.WriteString(name)
			if len(labels) > 0 {
				sb.WriteByte('{')
				sb.WriteString(strings.Join(labels, ","))
				sb.WriteByte('}')
			}
			sb.WriteByte(' ')
			sb.WriteString(fmt.Sprintf("%g", value))
			sb.WriteByte('\n')
		}

		// Helper to write a counter metric.
		counter := func(name, help string, value float64, labels ...string) {
			sb.WriteString("# HELP ")
			sb.WriteString(name)
			sb.WriteByte(' ')
			sb.WriteString(help)
			sb.WriteByte('\n')
			sb.WriteString("# TYPE ")
			sb.WriteString(name)
			sb.WriteString(" counter\n")
			sb.WriteString(name)
			if len(labels) > 0 {
				sb.WriteByte('{')
				sb.WriteString(strings.Join(labels, ","))
				sb.WriteByte('}')
			}
			sb.WriteByte(' ')
			sb.WriteString(fmt.Sprintf("%g", value))
			sb.WriteByte('\n')
		}

		if p.DroppedEvents != nil {
			counter("muxcore_event_bus_dropped_events_total",
				"Total events dropped due to full subscriber channels.",
				float64(p.DroppedEvents()))
		}

		if p.ActiveSubscribers != nil {
			gauge("muxcore_event_bus_active_subscribers",
				"Number of active event subscriptions.",
				float64(p.ActiveSubscribers()))
		}

		if p.ConnPoolSize != nil {
			gauge("muxcore_grpc_conn_pool_size",
				"Number of gRPC connections currently in the connection pool.",
				float64(p.ConnPoolSize()))
		}

		if p.RegistryModuleCount != nil {
			gauge("muxcore_registry_module_count",
				"Number of modules currently registered.",
				float64(p.RegistryModuleCount()))
		}

		if p.LeaderTerm != nil {
			gauge("muxcore_cluster_leader_term",
				"Current cluster leader election term.",
				float64(p.LeaderTerm()))
		}

		if p.IsLeader != nil {
			isLeader := 0.0
			if p.IsLeader() {
				isLeader = 1.0
			}
			gauge("muxcore_cluster_is_leader",
				"1 if this node is the cluster leader, 0 otherwise.",
				isLeader)
		}

		if p.StorageProviderCount != nil {
			gauge("muxcore_storage_provider_count",
				"Number of registered storage providers.",
				float64(p.StorageProviderCount()))
		}

		if p.ModuleDegradedCount != nil {
			gauge("muxcore_module_degraded_count",
				"Number of modules in degraded state.",
				float64(p.ModuleDegradedCount()))
		}

		if p.GoroutineCount != nil {
			gauge("muxcore_goroutine_count",
				"Current number of Go runtime goroutines.",
				float64(p.GoroutineCount()))
		}

		if p.AllocBytes != nil {
			gauge("muxcore_memory_alloc_bytes",
				"Current heap memory allocation in bytes.",
				float64(p.AllocBytes()))
		}

		fmt.Fprint(w, sb.String())
	}
}
