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
// Meta carries task-type-specific context that executors interpret.
type WorkerTask struct {
	ID            string
	Type          string
	Payload       []byte
	AssignedNode  string
	Status        WorkerTaskStatus
	MaxRetries    int
	RetryCount    int
	Capabilities  []string
	CreatedAt     time.Time
	StartedAt     time.Time
	CompletedAt   time.Time
	LastHeartbeat time.Time
	Error         string
	Meta          map[string]any

	// IdempotencyKey is an optional key that executors use with
	// IdempotencyProvider to prevent duplicate execution. When a task
	// is reassigned after a node failure, the new executor checks this
	// key before re-executing. If empty, no idempotency guard is applied.
	IdempotencyKey string

	// HeartbeatInterval is how often the executor promises to call
	// Heartbeat() while processing this task. The worker pool reaper
	// uses this to detect stalled executors. If zero, the pool's
	// DefaultHeartbeatTimeout is used.
	HeartbeatInterval time.Duration
}

// WorkerPool schedules tasks across cluster nodes and tracks their lifecycle.
type WorkerPool interface {
	Submit(ctx context.Context, task WorkerTask) (string, error)
	Status(ctx context.Context, taskID string) (WorkerTask, error)
	Cancel(ctx context.Context, taskID string) error
	List(ctx context.Context, filter *WorkerTaskFilter) ([]WorkerTask, error)

	// Reassign moves a task from a dead or unresponsive node to a new one.
	// If the task is already completed or cancelled, Reassign returns an error.
	// The new node should re-execute the task from scratch unless the executor
	// checks IdempotencyKey against an IdempotencyProvider.
	Reassign(ctx context.Context, taskID, newNode string) error
}

// WorkerTaskFilter narrows task queries.
type WorkerTaskFilter struct {
	Status       WorkerTaskStatus
	Type         string
	AssignedNode string
}

// Executor is implemented by modules that can run tasks of a given type.
type Executor interface {
	CanHandle(taskType string) bool
	Execute(ctx context.Context, task WorkerTask) (result []byte, err error)
}
