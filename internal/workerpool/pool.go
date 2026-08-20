// Package workerpool provides an in-memory WorkerPool that tracks
// distributed tasks and their lifecycle across cluster nodes.
package workerpool

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/Muxcore-Media/core/internal/registry"
	"github.com/Muxcore-Media/core/pkg/contracts"
)

// Default heartbeat timeout for running tasks.
const (
	DefaultHeartbeatTimeout = 30 * time.Second
	defaultReaperInterval   = 15 * time.Second
	// maxTasks is the maximum number of concurrent worker pool tasks.
	// Prevents unbounded map growth (DoS via Submit flooding).
	maxTasks = 100_000
)

// Pool implements contracts.WorkerPool with in-memory storage.
// It is safe for concurrent use and can be optionally backed by a
// storage provider for persistence across restarts.
type Pool struct { //nolint:govet // struct field alignment is acceptable
	mu               sync.RWMutex
	tasks            map[string]*contracts.WorkerTask
	nodeID           string
	heartbeatTimeout time.Duration // max age of LastHeartbeat before a task is reaped
	reaperCancel     context.CancelFunc
	store            TaskStore // optional; when set, tasks persist across restarts
}

// New creates an in-memory WorkerPool.
// nodeID identifies this node for task assignment tracking.
func New(nodeID string) *Pool {
	return &Pool{
		tasks:            make(map[string]*contracts.WorkerTask),
		nodeID:           nodeID,
		heartbeatTimeout: DefaultHeartbeatTimeout,
	}
}

// SetStore attaches a persistent TaskStore. When set, every task mutation
// is written through to the store, and tasks are loaded from the store
// on Start. Must be called before Start.
func (p *Pool) SetStore(s TaskStore) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.store = s
}

// SetHeartbeatTimeout configures how long without a heartbeat before a
// running task is considered stale and failed by the reaper.
func (p *Pool) SetHeartbeatTimeout(d time.Duration) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.heartbeatTimeout = d
}

// Start launches the background reaper goroutine that monitors task
// heartbeats and fails stale tasks. If a TaskStore is configured, tasks
// are loaded from the store first. The reaper runs until ctx is
// cancelled. Safe to call multiple times — subsequent calls are no-ops.
func (p *Pool) Start(ctx context.Context) {
	p.mu.Lock()
	store := p.store
	p.mu.Unlock()

	if store != nil {
		saved, err := store.Load(ctx)
		if err != nil {
			slog.Warn("workerpool: failed to load persisted tasks", "error", err)
		} else if len(saved) > 0 {
			p.mu.Lock()
			for _, task := range saved {
				// Only load non-terminal tasks — completed/failed/cancelled
				// tasks are already done and don't need to be re-tracked.
				switch task.Status {
				case contracts.WorkerTaskStatusPending,
					contracts.WorkerTaskStatusAssigned,
					contracts.WorkerTaskStatusRunning:
					p.tasks[task.ID] = task
				}
			}
			count := len(p.tasks)
			p.mu.Unlock()
			slog.Info("workerpool: loaded persisted tasks", "count", count)
		}
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	if p.reaperCancel != nil {
		return // already started
	}
	reaperCtx, cancel := context.WithCancel(ctx)
	p.reaperCancel = cancel
	go p.reaperLoop(reaperCtx)
	slog.Info("workerpool: reaper started", "timeout", p.heartbeatTimeout)
}

// Stop signals the reaper goroutine to shut down. Safe to call
// multiple times.
func (p *Pool) Stop() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.reaperCancel != nil {
		p.reaperCancel()
		p.reaperCancel = nil
	}
}

// Shutdown drains in-flight tasks, persists them for redelivery, then
// stops the reaper. Call during graceful core shutdown. The context
// controls how long to wait for in-flight tasks to complete before
// force-persisting them.
func (p *Pool) Shutdown(ctx context.Context) error {
	p.mu.Lock()
	reaperCancel := p.reaperCancel
	p.mu.Unlock()

	// Persist all Running/Assigned tasks as Pending for redelivery.
	p.mu.Lock()
	for _, task := range p.tasks {
		switch task.Status {
		case contracts.WorkerTaskStatusRunning,
			contracts.WorkerTaskStatusAssigned:
			task.Status = contracts.WorkerTaskStatusPending
			task.AssignedNode = ""
			task.Error = "node shutting down"
			p.persistTask(ctx, task)
		}
	}
	p.mu.Unlock()

	// Stop the reaper.
	if reaperCancel != nil {
		reaperCancel()
	}
	return nil
}

// reaperLoop periodically checks for stale task heartbeats and fails them.
func (p *Pool) reaperLoop(ctx context.Context) {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("workerpool reaper panic recovered", "panic", r)
		}
	}()
	interval := p.reaperInterval()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			slog.Info("workerpool: reaper stopped")
			return
		case <-ticker.C:
			p.reapStaleTasks(ctx)
		}
	}
}

// reaperInterval returns how often the reaper checks for stale tasks.
func (p *Pool) reaperInterval() time.Duration {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.heartbeatTimeout > 0 {
		interval := p.heartbeatTimeout / 2
		if interval < time.Second {
			interval = time.Second
		}
		return interval
	}
	return defaultReaperInterval
}

// reapStaleTasks finds all Running tasks whose LastHeartbeat is older than
// heartbeatTimeout and marks them as Failed.
func (p *Pool) reapStaleTasks(ctx context.Context) {
	p.mu.Lock()
	defer p.mu.Unlock()

	deadline := time.Now().Add(-p.heartbeatTimeout)
	var reaped int

	for _, task := range p.tasks {
		if task.Status != contracts.WorkerTaskStatusRunning {
			continue
		}
		if task.LastHeartbeat.After(deadline) {
			continue
		}
		task.Status = contracts.WorkerTaskStatusFailed
		task.Error = "heartbeat timeout"
		task.CompletedAt = time.Now()
		persistCtx, cancel := context.WithTimeout(context.Background(), persistTimeout)
		p.persistTask(persistCtx, task) //nolint:contextcheck // background persist — don't inherit request context
		cancel()
		reaped++
	}

	if reaped > 0 {
		slog.Warn("workerpool: reaper failed stale tasks", "count", reaped, "timeout", p.heartbeatTimeout)
	}
}

// FailNodeTasks releases Running/Assigned tasks on the given node back to
// Pending for redispatch, incrementing RetryCount. Tasks that exceed MaxRetries
// (when MaxRetries > 0) are marked Failed. Returns the number of tasks touched.
func (p *Pool) FailNodeTasks(ctx context.Context, nodeID string) int {
	p.mu.Lock()
	defer p.mu.Unlock()

	var released, failed int
	for _, task := range p.tasks {
		if task.AssignedNode != nodeID {
			continue
		}
		switch task.Status {
		case contracts.WorkerTaskStatusPending,
			contracts.WorkerTaskStatusAssigned,
			contracts.WorkerTaskStatusRunning:
			task.RetryCount++
			task.AssignedNode = ""
			if task.MaxRetries > 0 && task.RetryCount > task.MaxRetries {
				task.Status = contracts.WorkerTaskStatusFailed
				task.Error = fmt.Sprintf("node %q left the cluster; exceeded max retries (%d)", nodeID, task.MaxRetries)
				task.CompletedAt = time.Now()
				failed++
			} else {
				task.Status = contracts.WorkerTaskStatusPending
				task.Error = fmt.Sprintf("node %q left the cluster; released for redispatch", nodeID)
				task.CompletedAt = time.Time{}
				released++
			}
			persistCtx, cancel := context.WithTimeout(context.Background(), persistTimeout)
			p.persistTask(persistCtx, task) //nolint:contextcheck // background persist — don't inherit request context
			cancel()
		}
	}

	count := released + failed
	if count > 0 {
		slog.Warn("workerpool: released/failed tasks for departed node",
			"node", nodeID, "released", released, "failed", failed)
	}
	return count
}

// Submit enqueues a new task. If the task has no ID, one is generated.
// The task is stored with Status=Pending and CreatedAt set to now.
func (p *Pool) Submit(ctx context.Context, task contracts.WorkerTask) (string, error) {
	if task.ID == "" {
		task.ID = uuid.NewString()
	}
	task.Status = contracts.WorkerTaskStatusPending
	task.CreatedAt = time.Now()

	if task.Type == "" {
		return "", fmt.Errorf("workerpool: task type is required")
	}

	p.mu.Lock()
	// Enforce task cap to prevent unbounded map growth (DoS via Submit).
	if len(p.tasks) >= maxTasks {
		p.mu.Unlock()
		return "", fmt.Errorf("workerpool: maximum tasks (%d) reached", maxTasks)
	}
	p.tasks[task.ID] = &task
	p.persistTask(ctx, &task)
	p.mu.Unlock()

	slog.Debug("workerpool: task submitted", "task_id", task.ID, "type", task.Type)
	return task.ID, nil
}

// Status returns the current state of a task by ID.
// Returns an error if the task is not found.
func (p *Pool) Status(ctx context.Context, taskID string) (contracts.WorkerTask, error) {
	p.mu.RLock()
	task, ok := p.tasks[taskID]
	p.mu.RUnlock()

	if !ok {
		return contracts.WorkerTask{}, fmt.Errorf("workerpool: task %q not found", taskID)
	}
	return *task, nil
}

// Cancel marks a task as cancelled. Only tasks in Pending, Assigned, or
// Running state can be cancelled. Returns an error if the task is not
// found or is in a terminal state (Completed, Failed, Cancelled).
func (p *Pool) Cancel(ctx context.Context, taskID string) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	task, ok := p.tasks[taskID]
	if !ok {
		return fmt.Errorf("workerpool: task %q not found", taskID)
	}

	switch task.Status {
	case contracts.WorkerTaskStatusPending,
		contracts.WorkerTaskStatusAssigned,
		contracts.WorkerTaskStatusRunning:
		task.Status = contracts.WorkerTaskStatusCancelled
		p.persistTask(ctx, task)
		return nil
	default:
		return fmt.Errorf("workerpool: cannot cancel task %q in state %s", taskID, task.Status)
	}
}

// List returns tasks matching the given filter. All filter fields are
// optional — nil/empty fields match everything. When filter is nil,
// all tasks are returned. Never returns an error.
func (p *Pool) List(ctx context.Context, filter *contracts.WorkerTaskFilter) ([]contracts.WorkerTask, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()

	var result []contracts.WorkerTask
	for _, t := range p.tasks {
		if filter != nil {
			if filter.Status != "" && t.Status != filter.Status {
				continue
			}
			if filter.Type != "" && t.Type != filter.Type {
				continue
			}
			if filter.AssignedNode != "" && t.AssignedNode != filter.AssignedNode {
				continue
			}
		}
		result = append(result, *t)
	}

	if result == nil {
		return []contracts.WorkerTask{}, nil
	}
	return result, nil
}

// Reassign moves a task to a different node. Only tasks in Assigned or
// Running state can be reassigned. Returns an error if the task is in a
// terminal state or not found.
func (p *Pool) Reassign(ctx context.Context, taskID, newNode string) error {
	if newNode == "" {
		return fmt.Errorf("workerpool: newNode is required for reassignment")
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	task, ok := p.tasks[taskID]
	if !ok {
		return fmt.Errorf("workerpool: task %q not found", taskID)
	}

	switch task.Status {
	case contracts.WorkerTaskStatusAssigned, contracts.WorkerTaskStatusRunning:
		task.AssignedNode = newNode
		task.RetryCount++
		if task.RetryCount > task.MaxRetries && task.MaxRetries > 0 {
			task.Status = contracts.WorkerTaskStatusFailed
			task.Error = fmt.Sprintf("exceeded max retries (%d)", task.MaxRetries)
			slog.Warn("workerpool: task exceeded max retries",
				"task_id", taskID, "retries", task.RetryCount)
		} else {
			task.Status = contracts.WorkerTaskStatusAssigned
		}
		p.persistTask(ctx, task)
		return nil
	case contracts.WorkerTaskStatusPending:
		task.AssignedNode = newNode
		task.Status = contracts.WorkerTaskStatusAssigned
		p.persistTask(ctx, task)
		return nil
	case contracts.WorkerTaskStatusCompleted:
		return fmt.Errorf("workerpool: cannot reassign completed task %q", taskID)
	case contracts.WorkerTaskStatusFailed:
		return fmt.Errorf("workerpool: cannot reassign failed task %q", taskID)
	case contracts.WorkerTaskStatusCancelled:
		return fmt.Errorf("workerpool: cannot reassign cancelled task %q", taskID)
	default:
		return fmt.Errorf("workerpool: cannot reassign task %q in state %s", taskID, task.Status)
	}
}

// UpdateStatus transitions a task to a new status with an optional error message.
// Returns an error if the task is not found.
func (p *Pool) UpdateStatus(ctx context.Context, taskID string, status contracts.WorkerTaskStatus, errorMsg string) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	task, ok := p.tasks[taskID]
	if !ok {
		return fmt.Errorf("workerpool: task %q not found", taskID)
	}

	task.Status = status
	task.Error = errorMsg

	switch status {
	case contracts.WorkerTaskStatusRunning:
		task.StartedAt = time.Now()
	case contracts.WorkerTaskStatusCompleted:
		task.CompletedAt = time.Now()
	case contracts.WorkerTaskStatusFailed:
		task.CompletedAt = time.Now()
	case contracts.WorkerTaskStatusAssigned:
		task.AssignedNode = p.nodeID
	}

	p.persistTask(ctx, task)
	return nil
}

// Heartbeat records a liveness signal for a running task.
// Returns an error if the task is not found or not in Running state.
func (p *Pool) Heartbeat(ctx context.Context, taskID string) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	task, ok := p.tasks[taskID]
	if !ok {
		return fmt.Errorf("workerpool: task %q not found", taskID)
	}
	if task.Status != contracts.WorkerTaskStatusRunning {
		return fmt.Errorf("workerpool: task %q is not running (state: %s)", taskID, task.Status)
	}
	task.LastHeartbeat = time.Now()
	p.persistTask(ctx, task)
	return nil
}

// RegisterSelf creates a virtual module entry in the given Registry so that
// this WorkerPool is discoverable via FindByCapability("worker.pool").
// Other modules can then discover and use the pool at runtime.
// The virtual module has no lifecycle — RegisterSelf is idempotent.
func RegisterSelf(reg *registry.Registry, pool contracts.WorkerPool, nodeID string) {
	if reg == nil {
		slog.Warn("workerpool: RegisterSelf called with nil registry")
		return
	}
	// Wrap the pool as a contracts.Module so we can register it.
	m := &poolModule{pool: pool, nodeID: nodeID}
	if err := reg.Register(m, nil); err != nil && !strings.Contains(err.Error(), "already registered") {
		slog.Warn("workerpool: register self", "error", err)
	}
}

// poolModule wraps a WorkerPool to satisfy contracts.Module for registry
// registration. Lifecycle methods are no-ops — the pool runs in-process.
type poolModule struct {
	pool   contracts.WorkerPool
	nodeID string
}

func (m *poolModule) Info() contracts.ModuleInfo {
	return contracts.ModuleInfo{
		ID:           "core.workerpool",
		Name:         "core-workerpool",
		Version:      "1.0.0",
		Description:  "Core in-memory worker pool for distributed task tracking",
		Capabilities: []string{contracts.CapabilityWorkerPool},
	}
}

func (m *poolModule) Init(_ context.Context) error   { return nil }
func (m *poolModule) Start(_ context.Context) error  { return nil }
func (m *poolModule) Stop(_ context.Context) error   { return nil }
func (m *poolModule) Health(_ context.Context) error { return nil }

// Count returns the total number of tracked tasks.
func (p *Pool) Count() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return len(p.tasks)
}

// persistTimeout is the maximum time to wait for a single task persistence
// operation. Prevents persistTask from blocking indefinitely when called
// from background paths (reaper, fail-node) that don't have a request context.
// persistTimeout is the maximum time to wait for a single task persistence
// operation. Prevents persistTask from blocking indefinitely when called
// from background paths (reaper, fail-node) that don't have a request context.
const persistTimeout = 5 * time.Second

// persistTask writes a task to the store if one is configured.
// Must be called with p.mu already held (read or write).
func (p *Pool) persistTask(ctx context.Context, task *contracts.WorkerTask) {
	if p.store == nil {
		return
	}
	if err := p.store.Save(ctx, task); err != nil {
		slog.Warn("workerpool: persist task failed", "task_id", task.ID, "error", err)
	}
}

// compile-time interface checks
var _ contracts.WorkerPool = (*Pool)(nil)
