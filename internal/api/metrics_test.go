package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMetricsHandler_AllNilProviders(t *testing.T) {
	h := MetricsHandler(&MetricsProvider{})
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/metrics", nil)
	rec := httptest.NewRecorder()
	h(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}
	// With nil providers the body should be empty (no metrics written).
	if rec.Body.Len() != 0 {
		t.Errorf("expected empty body with nil providers, got %q", rec.Body.String())
	}
}

func TestMetricsHandler_DroppedEvents(t *testing.T) {
	h := MetricsHandler(&MetricsProvider{
		DroppedEvents: func() int64 { return 42 },
	})
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/metrics", nil)
	rec := httptest.NewRecorder()
	h(rec, req)

	body := rec.Body.String()
	if !strings.Contains(body, "muxcore_event_bus_dropped_events_total") {
		t.Error("expected dropped events metric in output")
	}
	if !strings.Contains(body, "42") {
		t.Errorf("expected value 42 in output, got:\n%s", body)
	}
}

func TestMetricsHandler_ActiveSubscribers(t *testing.T) {
	h := MetricsHandler(&MetricsProvider{
		ActiveSubscribers: func() int { return 7 },
	})
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/metrics", nil)
	rec := httptest.NewRecorder()
	h(rec, req)

	body := rec.Body.String()
	if !strings.Contains(body, "muxcore_event_bus_active_subscribers") {
		t.Error("expected active subscribers metric")
	}
	if !strings.Contains(body, "7") {
		t.Errorf("expected 7 in output, got:\n%s", body)
	}
}

func TestMetricsHandler_ConnPoolSize(t *testing.T) {
	h := MetricsHandler(&MetricsProvider{
		ConnPoolSize: func() int { return 3 },
	})
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/metrics", nil)
	rec := httptest.NewRecorder()
	h(rec, req)

	body := rec.Body.String()
	if !strings.Contains(body, "muxcore_grpc_conn_pool_size") {
		t.Error("expected conn pool size metric")
	}
}

func TestMetricsHandler_RegistryModuleCount(t *testing.T) {
	h := MetricsHandler(&MetricsProvider{
		RegistryModuleCount: func() int { return 5 },
	})
	rec := httptest.NewRecorder()
	h(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/metrics", nil))

	if !strings.Contains(rec.Body.String(), "muxcore_registry_module_count") {
		t.Error("expected registry module count metric")
	}
}

func TestMetricsHandler_LeaderTerm(t *testing.T) {
	h := MetricsHandler(&MetricsProvider{
		LeaderTerm: func() uint64 { return 9 },
	})
	rec := httptest.NewRecorder()
	h(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/metrics", nil))

	body := rec.Body.String()
	if !strings.Contains(body, "muxcore_cluster_leader_term") {
		t.Error("expected leader term metric")
	}
	if !strings.Contains(body, "9") {
		t.Errorf("expected term 9, got:\n%s", body)
	}
}

func TestMetricsHandler_IsLeader_True(t *testing.T) {
	h := MetricsHandler(&MetricsProvider{
		IsLeader: func() bool { return true },
	})
	rec := httptest.NewRecorder()
	h(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/metrics", nil))

	body := rec.Body.String()
	if !strings.Contains(body, "muxcore_cluster_is_leader") {
		t.Error("expected is_leader metric")
	}
	if !strings.Contains(body, " 1\n") {
		t.Errorf("expected is_leader=1 when leader, got:\n%s", body)
	}
}

func TestMetricsHandler_IsLeader_False(t *testing.T) {
	h := MetricsHandler(&MetricsProvider{
		IsLeader: func() bool { return false },
	})
	rec := httptest.NewRecorder()
	h(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/metrics", nil))

	body := rec.Body.String()
	if !strings.Contains(body, " 0\n") {
		t.Errorf("expected is_leader=0 when not leader, got:\n%s", body)
	}
}

func TestMetricsHandler_ContentType(t *testing.T) {
	h := MetricsHandler(&MetricsProvider{
		DroppedEvents: func() int64 { return 0 },
	})
	rec := httptest.NewRecorder()
	h(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/metrics", nil))

	ct := rec.Header().Get("Content-Type")
	if !strings.HasPrefix(ct, "text/plain") {
		t.Errorf("expected text/plain Content-Type, got %q", ct)
	}
}

func TestMetricsHandler_AuthFailureCount(t *testing.T) {
	h := MetricsHandler(&MetricsProvider{
		AuthFailureCount: func() int64 { return 7 },
	})
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/metrics", nil)
	rec := httptest.NewRecorder()
	h(rec, req)

	body := rec.Body.String()
	if !strings.Contains(body, "muxcore_api_auth_failures_total") {
		t.Error("expected auth failure metric in output")
	}
	if !strings.Contains(body, "7") {
		t.Errorf("expected value 7 in output, got:\n%s", body)
	}
}

func TestMetricsHandler_BackoffActiveCount(t *testing.T) {
	h := MetricsHandler(&MetricsProvider{
		BackoffActiveCount: func() int { return 2 },
	})
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/metrics", nil)
	rec := httptest.NewRecorder()
	h(rec, req)

	body := rec.Body.String()
	if !strings.Contains(body, "muxcore_api_auth_backoffs_active") {
		t.Error("expected backoff active metric in output")
	}
	if !strings.Contains(body, "2") {
		t.Errorf("expected value 2 in output, got:\n%s", body)
	}
}

func TestMetricsHandler_MeshCallCount(t *testing.T) {
	h := MetricsHandler(&MetricsProvider{
		MeshCallCount: func() int64 { return 99 },
	})
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/metrics", nil)
	rec := httptest.NewRecorder()
	h(rec, req)

	body := rec.Body.String()
	if !strings.Contains(body, "muxcore_grpc_mesh_calls_total") {
		t.Error("expected mesh call count metric in output")
	}
	if !strings.Contains(body, "99") {
		t.Errorf("expected value 99 in output, got:\n%s", body)
	}
}

func TestMetricsHandler_PublishCount(t *testing.T) {
	h := MetricsHandler(&MetricsProvider{
		PublishCount: func() int64 { return 77 },
	})
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/metrics", nil)
	rec := httptest.NewRecorder()
	h(rec, req)

	body := rec.Body.String()
	if !strings.Contains(body, "muxcore_event_bus_publishes_total") {
		t.Error("expected publish count metric in output")
	}
	if !strings.Contains(body, "77") {
		t.Errorf("expected value 77 in output, got:\n%s", body)
	}
}

func TestMetricsHandler_RequestCount(t *testing.T) {
	h := MetricsHandler(&MetricsProvider{
		RequestCount: func() int64 { return 33 },
	})
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/metrics", nil)
	rec := httptest.NewRecorder()
	h(rec, req)

	body := rec.Body.String()
	if !strings.Contains(body, "muxcore_api_requests_total") {
		t.Error("expected request count metric in output")
	}
	if !strings.Contains(body, "33") {
		t.Errorf("expected value 33 in output, got:\n%s", body)
	}
}

func TestMetricsHandler_StorageCounters(t *testing.T) {
	h := MetricsHandler(&MetricsProvider{
		StoragePutCount:    func() int64 { return 10 },
		StorageCount:       func() int64 { return 20 },
		StorageDeleteCount: func() int64 { return 5 },
	})
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/metrics", nil)
	rec := httptest.NewRecorder()
	h(rec, req)

	body := rec.Body.String()
	for _, name := range []string{
		"muxcore_storage_put_operations_total",
		"muxcore_storage_get_operations_total",
		"muxcore_storage_delete_operations_total",
	} {
		if !strings.Contains(body, name) {
			t.Errorf("missing metric %q in output", name)
		}
	}
	if !strings.Contains(body, "10") || !strings.Contains(body, "20") || !strings.Contains(body, "5") {
		t.Errorf("expected values 10, 20, 5 in output, got:\n%s", body)
	}
}

func TestMetricsHandler_ModuleCounters(t *testing.T) {
	h := MetricsHandler(&MetricsProvider{
		ModuleSpawnCount:   func() int64 { return 3 },
		ModuleRestartCount: func() int64 { return 1 },
		ModuleResolveCount: func() int64 { return 5 },
	})
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/metrics", nil)
	rec := httptest.NewRecorder()
	h(rec, req)

	body := rec.Body.String()
	for _, name := range []string{
		"muxcore_module_spawns_total",
		"muxcore_module_restarts_total",
		"muxcore_module_resolves_total",
	} {
		if !strings.Contains(body, name) {
			t.Errorf("missing metric %q in output", name)
		}
	}
}

func TestMetricsHandler_StatusCounters(t *testing.T) {
	h := MetricsHandler(&MetricsProvider{
		StatusHTTP2xx: func() int64 { return 100 },
		StatusHTTP4xx: func() int64 { return 5 },
		StatusHTTP5xx: func() int64 { return 1 },
	})
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/metrics", nil)
	rec := httptest.NewRecorder()
	h(rec, req)

	body := rec.Body.String()
	for _, name := range []string{
		"muxcore_api_requests_2xx_total",
		"muxcore_api_requests_4xx_total",
		"muxcore_api_requests_5xx_total",
	} {
		if !strings.Contains(body, name) {
			t.Errorf("missing metric %q in output", name)
		}
	}
}

func TestMetricsHandler_PrometheusFormat(t *testing.T) {
	h := MetricsHandler(&MetricsProvider{
		DroppedEvents: func() int64 { return 1 },
	})
	rec := httptest.NewRecorder()
	h(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/metrics", nil))

	body := rec.Body.String()
	// Prometheus format requires # HELP and # TYPE lines.
	if !strings.Contains(body, "# HELP ") {
		t.Error("expected # HELP lines in Prometheus output")
	}
	if !strings.Contains(body, "# TYPE ") {
		t.Error("expected # TYPE lines in Prometheus output")
	}
}

func TestMetricsHandler_AllMetrics(t *testing.T) {
	h := MetricsHandler(&MetricsProvider{
		DroppedEvents:       func() int64 { return 1 },
		ActiveSubscribers:   func() int { return 2 },
		ConnPoolSize:        func() int { return 3 },
		RegistryModuleCount: func() int { return 4 },
		LeaderTerm:          func() uint64 { return 5 },
		IsLeader:            func() bool { return true },
		AuthFailureCount:    func() int64 { return 42 },
		BackoffActiveCount:  func() int { return 3 },
		MeshCallCount:       func() int64 { return 15 },
		PublishCount:        func() int64 { return 7 },
		RequestCount:        func() int64 { return 3 },
		StoragePutCount:     func() int64 { return 1 },
		StorageCount:        func() int64 { return 2 },
		StorageDeleteCount:  func() int64 { return 0 },
		StatusHTTP2xx:       func() int64 { return 10 },
		StatusHTTP4xx:       func() int64 { return 2 },
		StatusHTTP5xx:       func() int64 { return 0 },
		ModuleSpawnCount:    func() int64 { return 1 },
		ModuleRestartCount:  func() int64 { return 0 },
		ModuleResolveCount:  func() int64 { return 2 },
	})
	rec := httptest.NewRecorder()
	h(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/metrics", nil))

	expectedMetrics := []string{
		"muxcore_event_bus_dropped_events_total",
		"muxcore_event_bus_active_subscribers",
		"muxcore_grpc_conn_pool_size",
		"muxcore_registry_module_count",
		"muxcore_cluster_leader_term",
		"muxcore_cluster_is_leader",
		"muxcore_api_auth_failures_total",
		"muxcore_api_auth_backoffs_active",
		"muxcore_grpc_mesh_calls_total",
		"muxcore_event_bus_publishes_total",
		"muxcore_api_requests_total",
		"muxcore_storage_put_operations_total",
		"muxcore_storage_get_operations_total",
		"muxcore_api_requests_2xx_total",
		"muxcore_api_requests_4xx_total",
		"muxcore_module_spawns_total",
		"muxcore_module_resolves_total",
	}
	body := rec.Body.String()
	for _, name := range expectedMetrics {
		if !strings.Contains(body, name) {
			t.Errorf("missing metric %q in output", name)
		}
	}
}
