package workerpool

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/Muxcore-Media/core/pkg/contracts"
)

const defaultDispatchInterval = 5 * time.Second

// Dispatcher polls the WorkerPool for pending tasks and routes them to
// registered executor modules via the gRPC mesh.
type Dispatcher struct {
	pool     *Pool
	registry contracts.Registry
	mesh     MeshCaller
	interval time.Duration
}

// MeshCaller is the subset of ModuleMeshClient the dispatcher needs.
type MeshCaller interface {
	Call(ctx context.Context, targetModule, method string, payload []byte) ([]byte, error)
}

// NewDispatcher creates a dispatcher that routes pending tasks to executors.
func NewDispatcher(pool *Pool, registry contracts.Registry, mesh MeshCaller) *Dispatcher {
	return &Dispatcher{
		pool:     pool,
		registry: registry,
		mesh:     mesh,
		interval: defaultDispatchInterval,
	}
}

// SetInterval overrides the default dispatch polling interval.
func (d *Dispatcher) SetInterval(interval time.Duration) {
	if interval > 0 {
		d.interval = interval
	}
}

// Start begins the dispatch loop. Runs until ctx is cancelled.
func (d *Dispatcher) Start(ctx context.Context) {
	go d.loop(ctx)
	slog.Info("dispatcher: started", "interval", d.interval)
}

func (d *Dispatcher) loop(ctx context.Context) {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("dispatcher loop panic recovered", "panic", r)
		}
	}()
	ticker := time.NewTicker(d.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			slog.Info("dispatcher: stopped")
			return
		case <-ticker.C:
			d.dispatchOnce(ctx)
		}
	}
}

func (d *Dispatcher) dispatchOnce(ctx context.Context) {
	if d.pool == nil {
		slog.Error("dispatcher: pool is nil, stopping dispatch")
		return
	}
	tasks, err := d.pool.List(ctx, &contracts.WorkerTaskFilter{
		Status: contracts.WorkerTaskStatusPending,
	})
	if err != nil {
		slog.Warn("dispatcher: list tasks", "error", err)
		return
	}
	for _, task := range tasks {
		d.dispatchTask(ctx, task)
	}
}

func (d *Dispatcher) dispatchTask(ctx context.Context, task contracts.WorkerTask) {
	if d.registry == nil {
		slog.Error("dispatcher: registry is nil, cannot dispatch task", "task_id", task.ID)
		return
	}
	candidates := d.registry.FindByCapability(
		contracts.CapabilityExecutorPrefix + task.Type,
	)
	if len(candidates) == 0 {
		slog.Debug("dispatcher: no executors for task type",
			"task_id", task.ID, "type", task.Type)
		return
	}

	targetID := candidates[0].Info.ID

	if err := d.pool.UpdateStatus(ctx, task.ID,
		contracts.WorkerTaskStatusRunning, ""); err != nil {
		slog.Warn("dispatcher: update status to running",
			"task_id", task.ID, "error", err)
		return
	}

	payload, err := json.Marshal(task)
	if err != nil {
		slog.Warn("dispatcher: marshal task",
			"task_id", task.ID, "error", err)
		_ = d.pool.UpdateStatus(ctx, task.ID,
			contracts.WorkerTaskStatusFailed, err.Error())
		return
	}

	if d.mesh == nil {
		slog.Error("dispatcher: mesh is nil, cannot dispatch task", "task_id", task.ID)
		_ = d.pool.UpdateStatus(ctx, task.ID,
			contracts.WorkerTaskStatusFailed, "dispatcher mesh is nil")
		return
	}
	if _, err := d.mesh.Call(ctx, targetID, "Execute", payload); err != nil {
		slog.Warn("dispatcher: execute failed",
			"task_id", task.ID, "executor", targetID, "error", err)
		_ = d.pool.UpdateStatus(ctx, task.ID,
			contracts.WorkerTaskStatusFailed, err.Error())
		return
	}

	_ = d.pool.UpdateStatus(ctx, task.ID,
		contracts.WorkerTaskStatusCompleted, "")
	slog.Debug("dispatcher: task completed",
		"task_id", task.ID, "executor", targetID)
}
