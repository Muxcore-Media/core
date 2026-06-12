package workerpool

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Muxcore-Media/core/pkg/contracts"
)

func TestFileStore_SaveAndLoad(t *testing.T) {
	dir := t.TempDir()
	s, err := NewFileStore(dir)
	if err != nil {
		t.Fatalf("NewFileStore failed: %v", err)
	}

	task := &contracts.WorkerTask{
		ID:      "task-1",
		Type:    "download",
		Payload: []byte("hello"),
	}

	if err := s.Save(context.Background(), task); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	tasks, err := s.Load(context.Background())
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if len(tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(tasks))
	}
	if tasks[0].ID != "task-1" {
		t.Errorf("expected task-1, got %s", tasks[0].ID)
	}
	if string(tasks[0].Payload) != "hello" {
		t.Errorf("expected payload 'hello', got %q", tasks[0].Payload)
	}
}

func TestFileStore_LoadEmpty(t *testing.T) {
	dir := t.TempDir()
	s, err := NewFileStore(dir)
	if err != nil {
		t.Fatalf("NewFileStore failed: %v", err)
	}

	tasks, err := s.Load(context.Background())
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if len(tasks) != 0 {
		t.Errorf("expected 0 tasks, got %d", len(tasks))
	}
}

func TestFileStore_LoadNonExistentDir(t *testing.T) {
	s, err := NewFileStore("/nonexistent/path")
	if err == nil {
		t.Fatal("expected error for non-existent parent dir")
	}
	_ = s
}

func TestFileStore_Delete(t *testing.T) {
	dir := t.TempDir()
	s, err := NewFileStore(dir)
	if err != nil {
		t.Fatalf("NewFileStore failed: %v", err)
	}

	task := &contracts.WorkerTask{ID: "to-delete", Type: "test"}
	s.Save(context.Background(), task)

	tasks, _ := s.Load(context.Background())
	if len(tasks) != 1 {
		t.Fatalf("expected 1 task before delete, got %d", len(tasks))
	}

	if err := s.Delete(context.Background(), "to-delete"); err != nil {
		t.Fatalf("Delete failed: %v", err)
	}

	tasks, _ = s.Load(context.Background())
	if len(tasks) != 0 {
		t.Errorf("expected 0 tasks after delete, got %d", len(tasks))
	}
}

func TestFileStore_DeleteNonExistent(t *testing.T) {
	dir := t.TempDir()
	s, err := NewFileStore(dir)
	if err != nil {
		t.Fatalf("NewFileStore failed: %v", err)
	}
	if err := s.Delete(context.Background(), "never-existed"); err != nil {
		t.Errorf("expected nil for non-existent task, got %v", err)
	}
}

func TestFileStore_PreventsPathTraversal(t *testing.T) {
	dir := t.TempDir()
	s, err := NewFileStore(dir)
	if err != nil {
		t.Fatalf("NewFileStore failed: %v", err)
	}

	// Task ID containing path separators should be safely sanitized.
	id := filepath.Join("..", "escape")
	s.Save(context.Background(), &contracts.WorkerTask{
		ID:   id,
		Type: "test",
	})

	// Verify no files ended up outside the store directory.
	entries, _ := os.ReadDir(dir)
	if len(entries) == 0 {
		t.Fatal("expected at least one saved file")
	}
	// All files should be inside the store directory.
	for _, e := range entries {
		fullPath := filepath.Join(dir, e.Name())
		cleaned, err := filepath.Abs(fullPath)
		if err != nil {
			t.Fatalf("Abs failed: %v", err)
		}
		storeAbs, err := filepath.Abs(dir)
		if err != nil {
			t.Fatalf("Abs failed: %v", err)
		}
		if !strings.HasPrefix(cleaned, storeAbs) {
			t.Errorf("file escaped store directory: %s", fullPath)
		}
	}
}

func TestFileStore_Overwrite(t *testing.T) {
	dir := t.TempDir()
	s, err := NewFileStore(dir)
	if err != nil {
		t.Fatalf("NewFileStore failed: %v", err)
	}

	task := &contracts.WorkerTask{ID: "same-id", Type: "v1"}
	s.Save(context.Background(), task)

	task.Type = "v2"
	s.Save(context.Background(), task)

	tasks, _ := s.Load(context.Background())
	if len(tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(tasks))
	}
	if tasks[0].Type != "v2" {
		t.Errorf("expected v2, got %s", tasks[0].Type)
	}
}

func TestPool_PersistenceEndToEnd(t *testing.T) {
	dir := t.TempDir()
	store, err := NewFileStore(dir)
	if err != nil {
		t.Fatalf("NewFileStore failed: %v", err)
	}

	// Create a pool, submit tasks, assign them.
	p1 := New("node-a")
	p1.SetStore(store)
	id1, _ := p1.Submit(context.Background(), contracts.WorkerTask{Type: "download"})
	id2, _ := p1.Submit(context.Background(), contracts.WorkerTask{Type: "transcode"})
	p1.Reassign(context.Background(), id1, "node-b")
	p1.UpdateStatus(context.Background(), id1, contracts.WorkerTaskStatusRunning, "")
	p1.Heartbeat(context.Background(), id1)
	p1.UpdateStatus(context.Background(), id2, contracts.WorkerTaskStatusCompleted, "done")

	// Create a fresh pool with the same store — should recover task 1 (Running)
	// but not task 2 (Completed, terminal, filtered).
	p2 := New("node-a")
	p2.SetStore(store)
	p2.Start(context.Background())
	defer p2.Stop()

	tasks, _ := p2.List(context.Background(), nil)
	if len(tasks) != 1 {
		t.Fatalf("expected 1 recovered task, got %d", len(tasks))
	}
	if tasks[0].ID != id1 {
		t.Errorf("expected recovered task %s, got %s", id1, tasks[0].ID)
	}
	if tasks[0].Status != contracts.WorkerTaskStatusRunning {
		t.Errorf("expected running, got %s", tasks[0].Status)
	}
}
