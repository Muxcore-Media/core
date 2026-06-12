package api

import (
	"fmt"
	"log/slog"
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
	AuthFailureCount     func() int64
	BackoffActiveCount   func() int
	MeshCallCount        func() int64
	PublishCount         func() int64
	RequestCount         func() int64
	StoragePutCount      func() int64
	StorageCount         func() int64
	StorageDeleteCount   func() int64
	ModuleSpawnCount     func() int64
	ModuleRestartCount   func() int64
	ModuleResolveCount   func() int64
	StatusHTTP2xx        func() int64
	StatusHTTP4xx        func() int64
	StatusHTTP5xx        func() int64
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
			fmt.Fprintf(&sb, "%g", value)
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
			fmt.Fprintf(&sb, "%g", value)
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

		if p.AuthFailureCount != nil {
			counter("muxcore_api_auth_failures_total",
				"Total number of authentication failures across all IPs.",
				float64(p.AuthFailureCount()))
		}

		if p.BackoffActiveCount != nil {
			gauge("muxcore_api_auth_backoffs_active",
				"Number of IPs currently in auth backoff.",
				float64(p.BackoffActiveCount()))
		}

		if p.MeshCallCount != nil {
			counter("muxcore_grpc_mesh_calls_total",
				"Total number of gRPC mesh calls made through the client.",
				float64(p.MeshCallCount()))
		}

		if p.PublishCount != nil {
			counter("muxcore_event_bus_publishes_total",
				"Total number of events published on the event bus.",
				float64(p.PublishCount()))
		}

		if p.RequestCount != nil {
			counter("muxcore_api_requests_total",
				"Total number of HTTP requests processed by the API server.",
				float64(p.RequestCount()))
		}

		if p.StoragePutCount != nil {
			counter("muxcore_storage_put_operations_total",
				"Total number of storage Put operations.",
				float64(p.StoragePutCount()))
		}

		if p.StorageCount != nil {
			counter("muxcore_storage_get_operations_total",
				"Total number of storage Get operations.",
				float64(p.StorageCount()))
		}

		if p.StorageDeleteCount != nil {
			counter("muxcore_storage_delete_operations_total",
				"Total number of storage Delete operations.",
				float64(p.StorageDeleteCount()))
		}

		if p.ModuleSpawnCount != nil {
			counter("muxcore_module_spawns_total",
				"Total number of module spawns.",
				float64(p.ModuleSpawnCount()))
		}

		if p.ModuleRestartCount != nil {
			counter("muxcore_module_restarts_total",
				"Total number of module restarts after crash.",
				float64(p.ModuleRestartCount()))
		}

		if p.ModuleResolveCount != nil {
			counter("muxcore_module_resolves_total",
				"Total number of module resolves (cache lookup + build).",
				float64(p.ModuleResolveCount()))
		}

		if p.StatusHTTP2xx != nil {
			counter("muxcore_api_requests_2xx_total",
				"Total number of HTTP 2xx responses.",
				float64(p.StatusHTTP2xx()))
		}

		if p.StatusHTTP4xx != nil {
			counter("muxcore_api_requests_4xx_total",
				"Total number of HTTP 4xx responses.",
				float64(p.StatusHTTP4xx()))
		}

		if p.StatusHTTP5xx != nil {
			counter("muxcore_api_requests_5xx_total",
				"Total number of HTTP 5xx responses.",
				float64(p.StatusHTTP5xx()))
		}

		if _, err := fmt.Fprint(w, sb.String()); err != nil {
			slog.Debug("metrics write failed", "error", err)
		}
	}
}
