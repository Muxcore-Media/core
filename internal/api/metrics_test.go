package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMetricsHandler_AllNilProviders(t *testing.T) {
	h := MetricsHandler(&MetricsProvider{})
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
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
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
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
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
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
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
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
	h(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))

	if !strings.Contains(rec.Body.String(), "muxcore_registry_module_count") {
		t.Error("expected registry module count metric")
	}
}

func TestMetricsHandler_LeaderTerm(t *testing.T) {
	h := MetricsHandler(&MetricsProvider{
		LeaderTerm: func() uint64 { return 9 },
	})
	rec := httptest.NewRecorder()
	h(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))

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
	h(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))

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
	h(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))

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
	h(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))

	ct := rec.Header().Get("Content-Type")
	if !strings.HasPrefix(ct, "text/plain") {
		t.Errorf("expected text/plain Content-Type, got %q", ct)
	}
}

func TestMetricsHandler_PrometheusFormat(t *testing.T) {
	h := MetricsHandler(&MetricsProvider{
		DroppedEvents: func() int64 { return 1 },
	})
	rec := httptest.NewRecorder()
	h(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))

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
	})
	rec := httptest.NewRecorder()
	h(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))

	expectedMetrics := []string{
		"muxcore_event_bus_dropped_events_total",
		"muxcore_event_bus_active_subscribers",
		"muxcore_grpc_conn_pool_size",
		"muxcore_registry_module_count",
		"muxcore_cluster_leader_term",
		"muxcore_cluster_is_leader",
	}
	body := rec.Body.String()
	for _, name := range expectedMetrics {
		if !strings.Contains(body, name) {
			t.Errorf("missing metric %q in output", name)
		}
	}
}
