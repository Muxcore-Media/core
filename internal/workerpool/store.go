package workerpool

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/Muxcore-Media/core/pkg/contracts"
)

// TaskStore persists WorkerTask state for recovery across restarts.
type TaskStore interface {
	// Save persists a task. Called after every mutation.
	Save(ctx context.Context, task *contracts.WorkerTask) error
	// Load reads all persisted tasks from the store.
	Load(ctx context.Context) ([]*contracts.WorkerTask, error)
	// Delete removes a task from the store (called for terminal states).
	Delete(ctx context.Context, taskID string) error
}

// FileStore implements TaskStore using individual JSON files per task.
// Each task is stored as {dir}/{taskID}.json.
type FileStore struct {
	dir string
}

// NewFileStore creates a FileStore rooted at dir. The directory is created
// if it does not exist. Returns an error if the directory cannot be created.
func NewFileStore(dir string) (*FileStore, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("workerpool: create task store dir %q: %w", dir, err)
	}
	return &FileStore{dir: dir}, nil
}

// Save writes a task as a JSON file. Overwrites any existing file for the
// same task ID.
func (f *FileStore) Save(_ context.Context, task *contracts.WorkerTask) error {
	if task.ID == "" {
		return fmt.Errorf("workerpool: cannot save task with empty ID")
	}
	path := filepath.Join(f.dir, safeFilename(task.ID)+".json")
	data, err := json.Marshal(task)
	if err != nil {
		return fmt.Errorf("workerpool: marshal task %q: %w", task.ID, err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil { //nolint:gosec // task data directory owned by core
		return fmt.Errorf("workerpool: write task %q: %w", task.ID, err)
	}
	return nil
}

// Load reads all .json files from the store directory and returns the
// parsed tasks. Files that cannot be parsed are skipped with a warning.
func (f *FileStore) Load(_ context.Context) ([]*contracts.WorkerTask, error) {
	entries, err := os.ReadDir(f.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("workerpool: read task store dir %q: %w", f.dir, err)
	}

	var tasks []*contracts.WorkerTask
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		path := filepath.Join(f.dir, e.Name())
		data, err := os.ReadFile(path) //nolint:gosec // task data directory owned by core
		if err != nil {
			slog.Warn("workerpool: skip unreadable task file", "path", path, "error", err)
			continue
		}
		var task contracts.WorkerTask
		if err := json.Unmarshal(data, &task); err != nil {
			slog.Warn("workerpool: skip unparseable task file", "path", path, "error", err)
			continue
		}
		tasks = append(tasks, &task)
	}

	return tasks, nil
}

// Delete removes a task's JSON file from the store. Returns nil if the
// file does not exist (already cleaned up).
func (f *FileStore) Delete(_ context.Context, taskID string) error {
	path := filepath.Join(f.dir, safeFilename(taskID)+".json")
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("workerpool: delete task %q: %w", taskID, err)
	}
	return nil
}

// safeFilename sanitizes a task ID for use as a filename. Replaces any
// path separator or special characters with underscores.
func safeFilename(id string) string {
	return strings.Map(func(r rune) rune {
		if r == '/' || r == '\\' || r == ':' || r == '*' || r == '?' || r == '"' || r == '<' || r == '>' || r == '|' || r == '\x00' {
			return '_'
		}
		return r
	}, id)
}
