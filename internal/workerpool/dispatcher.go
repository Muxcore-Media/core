package workerpool

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/Muxcore-Media/core/pkg/contracts"
)

const defaultDispatchInterval = 5 * time.Second

const metaExecutorKey = "executor"

// NodeResolver maps an executor module ID to the cluster node that hosts it.
// Returns "" when the module's host is unknown or not a current member.
type NodeResolver interface {
	NodeForModule(moduleID string) string
}

// Dispatcher polls the WorkerPool for pending tasks and routes them to
// registered executor modules via the gRPC mesh.
type Dispatcher struct {
	pool     *Pool
	registry contracts.Registry
	mesh     MeshCaller
	nodes    NodeResolver
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

// SetNodeResolver attaches a resolver used to set AssignedNode and skip
// executors whose host is unknown or departed.
func (d *Dispatcher) SetNodeResolver(r NodeResolver) {
	d.nodes = r
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

	targetID, assignedNode := d.pickExecutor(candidates)
	if targetID == "" {
		slog.Debug("dispatcher: no live executors for task type",
			"task_id", task.ID, "type", task.Type)
		return
	}

	if err := d.pool.beginDispatch(ctx, task.ID, assignedNode, targetID); err != nil {
		slog.Warn("dispatcher: begin dispatch",
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

// pickExecutor selects the first candidate whose host is known when a
// NodeResolver is configured. Without a resolver, the first candidate wins.
func (d *Dispatcher) pickExecutor(candidates []contracts.ModuleEntry) (moduleID, nodeID string) {
	for _, c := range candidates {
		id := c.Info.ID
		if d.nodes == nil {
			return id, ""
		}
		host := d.nodes.NodeForModule(id)
		if host == "" {
			continue
		}
		return id, host
	}
	return "", ""
}

// beginDispatch marks a pending task Running and records assignment metadata.
func (p *Pool) beginDispatch(ctx context.Context, taskID, assignedNode, executorID string) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	task, ok := p.tasks[taskID]
	if !ok {
		return fmt.Errorf("workerpool: task %q not found", taskID)
	}
	if task.Status != contracts.WorkerTaskStatusPending {
		return fmt.Errorf("workerpool: task %q is not pending (state: %s)", taskID, task.Status)
	}

	task.Status = contracts.WorkerTaskStatusRunning
	task.StartedAt = time.Now()
	task.Error = ""
	if assignedNode != "" {
		task.AssignedNode = assignedNode
	}
	if executorID != "" {
		if task.Meta == nil {
			task.Meta = make(map[string]any)
		}
		task.Meta[metaExecutorKey] = executorID
	}
	p.persistTask(ctx, task)
	return nil
}
