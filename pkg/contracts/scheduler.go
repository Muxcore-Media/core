package contracts

import "context"

type Scheduler interface {
	Schedule(ctx context.Context, task Task) (string, error)
	Cancel(ctx context.Context, taskID string) error
	Status(ctx context.Context, taskID string) (TaskStatus, error)
}

type Task struct {
	ID       string
	Name     string
	CronExpr string
	Handler  string
	Payload  []byte
	Timeout  int
}

type TaskStatus string

const (
	TaskScheduled TaskStatus = "scheduled"
	TaskRunning   TaskStatus = "running"
	TaskCompleted TaskStatus = "completed"
	TaskFailed    TaskStatus = "failed"
	TaskCancelled TaskStatus = "cancelled"
)
