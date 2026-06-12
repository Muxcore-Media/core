package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/Muxcore-Media/core/internal/workerpool"
	"github.com/Muxcore-Media/core/pkg/contracts"
)

// TaskHandlers registers HTTP endpoints for worker pool task management.
type TaskHandlers struct {
	pool *workerpool.Pool
}

// NewTaskHandlers creates handler functions backed by the given pool.
func NewTaskHandlers(pool *workerpool.Pool) *TaskHandlers {
	return &TaskHandlers{pool: pool}
}

// RegisterRoutes attaches task management endpoints to the server.
// All routes are under /api/v1/tasks.
func (h *TaskHandlers) RegisterRoutes(srv *Server) {
	srv.HandleFunc("/api/v1/tasks", h.handleListTasks)
	srv.HandleFunc("/api/v1/tasks/", h.handleTaskByID)
}

// handleListTasks responds to GET /api/v1/tasks.
// Query params: status, type, node (all optional).
func (h *TaskHandlers) handleListTasks(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	filter := &contracts.WorkerTaskFilter{}
	if s := r.URL.Query().Get("status"); s != "" {
		filter.Status = contracts.WorkerTaskStatus(s)
	}
	if t := r.URL.Query().Get("type"); t != "" {
		filter.Type = t
	}
	if n := r.URL.Query().Get("node"); n != "" {
		filter.AssignedNode = n
	}

	tasks, err := h.pool.List(r.Context(), filter)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"tasks": tasks,
		"count": len(tasks),
	})
}

// handleTaskByID routes to task detail, cancel, or reassign based on method
// and path suffix. Path: /api/v1/tasks/<id>[/cancel|/reassign].
func (h *TaskHandlers) handleTaskByID(w http.ResponseWriter, r *http.Request) {
	// Parse path: expected /api/v1/tasks/<id> or /api/v1/tasks/<id>/<action>
	path := strings.TrimPrefix(r.URL.Path, "/api/v1/tasks/")
	parts := strings.SplitN(path, "/", 2)

	taskID := parts[0]
	if taskID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "task ID is required"})
		return
	}

	action := ""
	if len(parts) > 1 {
		action = parts[1]
	}

	switch {
	case action == "" && r.Method == http.MethodGet:
		h.handleGetTask(w, r, taskID)
	case action == "cancel" && r.Method == http.MethodPost:
		h.handleCancelTask(w, r, taskID)
	case action == "reassign" && r.Method == http.MethodPost:
		h.handleReassignTask(w, r, taskID)
	default:
		http.Error(w, "not found", http.StatusNotFound)
	}
}

// handleGetTask responds to GET /api/v1/tasks/<id>.
func (h *TaskHandlers) handleGetTask(w http.ResponseWriter, r *http.Request, taskID string) {
	task, err := h.pool.Status(r.Context(), taskID)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"task": task})
}

// handleCancelTask responds to POST /api/v1/tasks/<id>/cancel.
func (h *TaskHandlers) handleCancelTask(w http.ResponseWriter, r *http.Request, taskID string) {
	if err := h.pool.Cancel(r.Context(), taskID); err != nil {
		writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "cancelled"})
}

// handleReassignTask responds to POST /api/v1/tasks/<id>/reassign.
// Body: {"node": "new-node-id"}.
func (h *TaskHandlers) handleReassignTask(w http.ResponseWriter, r *http.Request, taskID string) {
	var body struct {
		Node string `json:"node"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": fmt.Sprintf("invalid JSON body: %v", err)})
		return
	}
	if body.Node == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "node is required in JSON body"})
		return
	}

	if err := h.pool.Reassign(r.Context(), taskID, body.Node); err != nil {
		writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "reassigned", "node": body.Node})
}
