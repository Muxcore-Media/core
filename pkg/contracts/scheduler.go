package contracts

import (
	"context"
	"time"
)

// Scheduler is implemented by scheduler modules (scheduler-cron, etc.)
// to provide task scheduling. Core defines the contract; modules provide the driver.
//
// Discovered via FindByCapability(CapabilityScheduler).
type Scheduler interface {
	Schedule(ctx context.Context, task SchedulerTask) (string, error)
	Cancel(ctx context.Context, taskID string) error
	Status(ctx context.Context, taskID string) (SchedulerTaskStatus, error)
	// List returns all scheduled tasks matching the filter. Pass nil to list all.
	List(ctx context.Context, filter *SchedulerTaskFilter) ([]SchedulerTask, error)
}

// SchedulerTask is a unit of scheduled work.
// Payload carries the task data. Meta carries scheduler-specific
// or task-type-specific context that the scheduler module can interpret.
type SchedulerTask struct {
	ID       string
	Name     string
	CronExpr string
	Payload  []byte
	Timeout  time.Duration
	Meta     map[string]any
}

// SchedulerTaskFilter narrows List queries.
type SchedulerTaskFilter struct {
	Status SchedulerTaskStatus
	Name   string // substring match on task name; empty matches all
}

// SchedulerTaskStatus represents the lifecycle of a scheduled task.
type SchedulerTaskStatus string

const (
	SchedulerTaskScheduled SchedulerTaskStatus = "scheduled"
	SchedulerTaskRunning   SchedulerTaskStatus = "running"
	SchedulerTaskCompleted SchedulerTaskStatus = "completed"
	SchedulerTaskFailed    SchedulerTaskStatus = "failed"
	SchedulerTaskCancelled SchedulerTaskStatus = "cancelled"
)
