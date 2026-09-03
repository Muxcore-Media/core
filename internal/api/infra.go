package api

import (
	"net/http"
	"strings"

	corehealth "github.com/Muxcore-Media/core/internal/health"
	modmetrics "github.com/Muxcore-Media/core/internal/module"
	"github.com/Muxcore-Media/core/internal/registry"
)

// InfraHandlers exposes deep infrastructure introspection endpoints.
type InfraHandlers struct {
	Registry   *registry.Registry
	History    *corehealth.History
	RPCMetrics *modmetrics.RPCMetrics
}

// RegisterRoutes mounts infrastructure API routes on the server.
func (h *InfraHandlers) RegisterRoutes(s *Server) {
	if h == nil {
		return
	}
	s.HandleFunc("/api/v1/infra/modules/graph", h.handleModuleGraph)
	s.HandleFunc("/api/v1/infra/modules/health-history", h.handleHealthHistory)
	s.HandleFunc("/api/v1/infra/modules/metrics", h.handleModuleMetrics)
}

func (h *InfraHandlers) handleModuleGraph(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if h.Registry == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "registry unavailable"})
		return
	}
	graph := h.Registry.BuildDependencyGraph()
	format := strings.ToLower(r.URL.Query().Get("format"))
	switch format {
	case "dot":
		w.Header().Set("Content-Type", "text/vnd.graphviz")
		_, _ = w.Write([]byte(graph.DOT()))
	case "mermaid":
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte(graph.Mermaid()))
	default:
		writeJSON(w, http.StatusOK, graph)
	}
}

func (h *InfraHandlers) handleHealthHistory(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if h.History == nil {
		writeJSON(w, http.StatusOK, map[string]any{"modules": map[string]any{}})
		return
	}
	moduleID := r.URL.Query().Get("module")
	if moduleID != "" {
		if err := ValidateModuleID(moduleID); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"module":  moduleID,
			"history": h.History.Snapshot(moduleID),
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"modules": h.History.SnapshotAll()})
}

func (h *InfraHandlers) handleModuleMetrics(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if h.RPCMetrics == nil {
		writeJSON(w, http.StatusOK, map[string]any{"modules": map[string]any{}})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"modules": h.RPCMetrics.Snapshot()})
}
