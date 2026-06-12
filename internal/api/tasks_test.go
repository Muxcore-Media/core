package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/Muxcore-Media/core/internal/workerpool"
	"github.com/Muxcore-Media/core/pkg/contracts"
)

func setupTaskTest(t *testing.T) (*Server, *workerpool.Pool) {
	t.Helper()
	os.Setenv("MUXCORE_DEV_TLS_SKIP", "true")
	srv := NewServer(":0", "", "")
	pool := workerpool.New("node-a")
	pool.Start(context.Background())
	t.Cleanup(pool.Stop)

	th := NewTaskHandlers(pool)
	th.RegisterRoutes(srv)
	return srv, pool
}

func TestTasks_ListEmpty(t *testing.T) {
	srv, _ := setupTaskTest(t)
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/tasks", nil)
	rec := httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var body struct {
		Tasks []any `json:"tasks"`
		Count int   `json:"count"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Count != 0 {
		t.Errorf("expected 0 tasks, got %d", body.Count)
	}
}

func TestTasks_ListWithTasks(t *testing.T) {
	srv, pool := setupTaskTest(t)
	pool.Submit(context.Background(), contracts.WorkerTask{Type: "download"})
	pool.Submit(context.Background(), contracts.WorkerTask{Type: "transcode"})

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/tasks", nil)
	rec := httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, req)

	var body struct {
		Tasks []any `json:"tasks"`
		Count int   `json:"count"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Count != 2 {
		t.Errorf("expected 2 tasks, got %d", body.Count)
	}
}

func TestTasks_ListFilterByType(t *testing.T) {
	srv, pool := setupTaskTest(t)
	pool.Submit(context.Background(), contracts.WorkerTask{Type: "download"})
	pool.Submit(context.Background(), contracts.WorkerTask{Type: "transcode"})

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/tasks?type=download", nil)
	rec := httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, req)

	var body struct {
		Tasks []any `json:"tasks"`
		Count int   `json:"count"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Count != 1 {
		t.Errorf("expected 1 download task, got %d", body.Count)
	}
}

func TestTasks_ListMethodNotAllowed(t *testing.T) {
	srv, _ := setupTaskTest(t)
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/tasks", nil)
	rec := httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", rec.Code)
	}
}

func TestTasks_GetByID(t *testing.T) {
	srv, pool := setupTaskTest(t)
	id, _ := pool.Submit(context.Background(), contracts.WorkerTask{Type: "download"})

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/tasks/"+id, nil)
	rec := httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var body struct {
		Task contracts.WorkerTask `json:"task"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Task.ID != id {
		t.Errorf("expected task %s, got %s", id, body.Task.ID)
	}
	if body.Task.Type != "download" {
		t.Errorf("expected type download, got %s", body.Task.Type)
	}
}

func TestTasks_GetByID_NotFound(t *testing.T) {
	srv, _ := setupTaskTest(t)
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/tasks/nonexistent", nil)
	rec := httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", rec.Code)
	}
}

func TestTasks_Cancel(t *testing.T) {
	srv, pool := setupTaskTest(t)
	id, _ := pool.Submit(context.Background(), contracts.WorkerTask{Type: "download"})

	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/tasks/"+id+"/cancel", nil)
	rec := httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	task, _ := pool.Status(context.Background(), id)
	if task.Status != contracts.WorkerTaskStatusCancelled {
		t.Errorf("expected cancelled, got %s", task.Status)
	}
}

func TestTasks_Cancel_Completed(t *testing.T) {
	srv, pool := setupTaskTest(t)
	id, _ := pool.Submit(context.Background(), contracts.WorkerTask{Type: "download"})
	pool.UpdateStatus(context.Background(), id, contracts.WorkerTaskStatusCompleted, "")

	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/tasks/"+id+"/cancel", nil)
	rec := httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusConflict {
		t.Errorf("expected 409, got %d", rec.Code)
	}
}

func TestTasks_Reassign(t *testing.T) {
	srv, pool := setupTaskTest(t)
	id, _ := pool.Submit(context.Background(), contracts.WorkerTask{Type: "download", AssignedNode: "node-b"})
	pool.UpdateStatus(context.Background(), id, contracts.WorkerTaskStatusRunning, "")

	body := bytes.NewReader([]byte(`{"node": "node-c"}`))
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/tasks/"+id+"/reassign", body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	task, _ := pool.Status(context.Background(), id)
	if task.AssignedNode != "node-c" {
		t.Errorf("expected node-c, got %s", task.AssignedNode)
	}
}

func TestTasks_Reassign_MissingNode(t *testing.T) {
	srv, pool := setupTaskTest(t)
	id, _ := pool.Submit(context.Background(), contracts.WorkerTask{Type: "download"})

	body := bytes.NewReader([]byte(`{}`))
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/tasks/"+id+"/reassign", body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

func TestTasks_Reassign_Completed(t *testing.T) {
	srv, pool := setupTaskTest(t)
	id, _ := pool.Submit(context.Background(), contracts.WorkerTask{Type: "download"})
	pool.UpdateStatus(context.Background(), id, contracts.WorkerTaskStatusCompleted, "")

	body := bytes.NewReader([]byte(`{"node": "node-c"}`))
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/tasks/"+id+"/reassign", body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusConflict {
		t.Errorf("expected 409, got %d", rec.Code)
	}
}
