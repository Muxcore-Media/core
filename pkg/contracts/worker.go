package contracts

import (
	"context"
	"time"
)

// WorkerTaskStatus represents the lifecycle of a distributed task.
type WorkerTaskStatus string

const (
	WorkerTaskStatusPending   WorkerTaskStatus = "pending"
	WorkerTaskStatusAssigned  WorkerTaskStatus = "assigned"
	WorkerTaskStatusRunning   WorkerTaskStatus = "running"
	WorkerTaskStatusCompleted WorkerTaskStatus = "completed"
	WorkerTaskStatusFailed    WorkerTaskStatus = "failed"
	WorkerTaskStatusCancelled WorkerTaskStatus = "cancelled"
)

// WorkerTask is a unit of work scheduled across the cluster.
type WorkerTask struct {
	ID            string
	Type          string
	Payload       []byte
	AssignedNode  string
	Status        WorkerTaskStatus
	MaxRetries    int
	RetryCount    int
	Capabilities  []string // required node capabilities (e.g., "gpu", "ssd")
	CreatedAt     time.Time
	StartedAt     time.Time
	CompletedAt   time.Time
	LastHeartbeat time.Time
	Error         string
}

// WorkerPool schedules tasks across cluster nodes and tracks their lifecycle.
type WorkerPool interface {
	// Submit queues a task for execution. Returns the task ID.
	Submit(ctx context.Context, task WorkerTask) (string, error)

	// Status returns the current state of a task.
	Status(ctx context.Context, taskID string) (WorkerTask, error)

	// Cancel stops a pending or running task.
	Cancel(ctx context.Context, taskID string) error

	// List returns tasks matching the given filter. Nil filter returns all.
	List(ctx context.Context, filter *WorkerTaskFilter) ([]WorkerTask, error)
}

// WorkerTaskFilter narrows task queries.
type WorkerTaskFilter struct {
	Status       WorkerTaskStatus
	Type         string
	AssignedNode string
}

// Executor is implemented by modules that can run tasks of a given type.
// When a WorkerPool assigns a task to a node, it finds an Executor on that
// node that declares the task type in its capabilities.
type Executor interface {
	// CanHandle returns true if this executor accepts the given task type.
	CanHandle(taskType string) bool

	// Execute runs the task and returns the result payload on success,
	// or an error on failure.
	Execute(ctx context.Context, task WorkerTask) (result []byte, err error)
}
