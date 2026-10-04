package workerpool

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Muxcore-Media/core/pkg/contracts"
)

type mockRegistry struct {
	entries map[string][]contracts.ModuleEntry
}

func (m *mockRegistry) FindByRole(role string) []contracts.ModuleEntry {
	return m.entries[role]
}

func (m *mockRegistry) FindByCapability(capability string) []contracts.ModuleEntry {
	return m.entries[capability]
}

func (m *mockRegistry) SupportsCapability(moduleID, capability string) bool {
	return false
}

func (m *mockRegistry) Resolve(id string) (contracts.ModuleEntry, error) {
	return contracts.ModuleEntry{}, errors.New("not implemented")
}

func (m *mockRegistry) ListAll() []contracts.ModuleEntry {
	return nil
}

func (m *mockRegistry) StartupOrder() ([]string, error) {
	return nil, nil
}

func (m *mockRegistry) DependencyGraph(id string) ([]string, error) {
	return nil, nil
}

type mockMeshCaller struct {
	callFn func(ctx context.Context, targetModule, method string, payload []byte) ([]byte, error)
}

func (m *mockMeshCaller) Call(ctx context.Context, targetModule, method string, payload []byte) ([]byte, error) {
	return m.callFn(ctx, targetModule, method, payload)
}

func dispatcherTestPool(t *testing.T) *Pool {
	t.Helper()
	return New("node-test")
}

func TestDispatcher_DispatchPendingTask(t *testing.T) {
	pool := dispatcherTestPool(t)
	ctx := context.Background()

	id, err := pool.Submit(ctx, contracts.WorkerTask{
		Type:    "download",
		Payload: []byte(`{"url":"http://example.com/file"}`),
	})
	if err != nil {
		t.Fatalf("Submit failed: %v", err)
	}

	reg := &mockRegistry{
		entries: map[string][]contracts.ModuleEntry{
			"executor.download": {
				{Info: contracts.ModuleInfo{ID: "downloader-test"}},
			},
		},
	}

	mockMesh := &mockMeshCaller{
		callFn: func(_ context.Context, targetModule, method string, payload []byte) ([]byte, error) {
			if targetModule != "downloader-test" {
				t.Fatalf("expected target module downloader-test, got %s", targetModule)
			}
			if method != "Execute" {
				t.Fatalf("expected method Execute, got %s", method)
			}
			return []byte(`{"result":"ok"}`), nil
		},
	}

	disp := NewDispatcher(pool, reg, mockMesh)
	disp.dispatchOnce(ctx)

	task, err := pool.Status(ctx, id)
	if err != nil {
		t.Fatalf("Status failed: %v", err)
	}
	if task.Status != contracts.WorkerTaskStatusCompleted {
		t.Fatalf("expected Completed, got %s", task.Status)
	}
}

func TestDispatcher_ExecutorFails(t *testing.T) {
	pool := dispatcherTestPool(t)
	ctx := context.Background()

	id, err := pool.Submit(ctx, contracts.WorkerTask{
		Type:    "download",
		Payload: []byte(`{"url":"http://example.com/file"}`),
	})
	if err != nil {
		t.Fatalf("Submit failed: %v", err)
	}

	reg := &mockRegistry{
		entries: map[string][]contracts.ModuleEntry{
			"executor.download": {
				{Info: contracts.ModuleInfo{ID: "downloader-broken"}},
			},
		},
	}

	mockMesh := &mockMeshCaller{
		callFn: func(_ context.Context, targetModule, method string, payload []byte) ([]byte, error) {
			return nil, errors.New("executor crashed")
		},
	}

	disp := NewDispatcher(pool, reg, mockMesh)
	disp.dispatchOnce(ctx)

	task, err := pool.Status(ctx, id)
	if err != nil {
		t.Fatalf("Status failed: %v", err)
	}
	if task.Status != contracts.WorkerTaskStatusFailed {
		t.Fatalf("expected Failed, got %s", task.Status)
	}
	if task.Error != "executor crashed" {
		t.Fatalf("expected error 'executor crashed', got %q", task.Error)
	}
}

func TestDispatcher_NoExecutorForType(t *testing.T) {
	pool := dispatcherTestPool(t)
	ctx := context.Background()

	id, err := pool.Submit(ctx, contracts.WorkerTask{
		Type:    "unknown-task",
		Payload: []byte(`{}`),
	})
	if err != nil {
		t.Fatalf("Submit failed: %v", err)
	}

	reg := &mockRegistry{entries: make(map[string][]contracts.ModuleEntry)}

	mockMesh := &mockMeshCaller{
		callFn: func(_ context.Context, _, _ string, _ []byte) ([]byte, error) {
			t.Fatal("Call should not be invoked when no executor is registered")
			return nil, nil
		},
	}

	disp := NewDispatcher(pool, reg, mockMesh)
	disp.dispatchOnce(ctx)

	task, err := pool.Status(ctx, id)
	if err != nil {
		t.Fatalf("Status failed: %v", err)
	}
	if task.Status != contracts.WorkerTaskStatusPending {
		t.Fatalf("expected Pending (not dispatched), got %s", task.Status)
	}
}

func TestDispatcher_MultipleTasks(t *testing.T) {
	pool := dispatcherTestPool(t)
	ctx := context.Background()

	ids := make([]string, 3)
	for i := range 3 {
		id, err := pool.Submit(ctx, contracts.WorkerTask{
			Type:    "transcode",
			Payload: []byte(`{"file":"video.mp4"}`),
		})
		if err != nil {
			t.Fatalf("Submit %d failed: %v", i, err)
		}
		ids[i] = id
	}

	var callCount int
	reg := &mockRegistry{
		entries: map[string][]contracts.ModuleEntry{
			"executor.transcode": {
				{Info: contracts.ModuleInfo{ID: "transcoder-test"}},
			},
		},
	}
	mockMesh := &mockMeshCaller{
		callFn: func(_ context.Context, targetModule, method string, payload []byte) ([]byte, error) {
			callCount++
			return []byte(`{"result":"ok"}`), nil
		},
	}

	disp := NewDispatcher(pool, reg, mockMesh)
	disp.dispatchOnce(ctx)

	if callCount != 3 {
		t.Fatalf("expected 3 Call invocations, got %d", callCount)
	}
	for _, id := range ids {
		task, err := pool.Status(ctx, id)
		if err != nil {
			t.Fatalf("Status for %s failed: %v", id, err)
		}
		if task.Status != contracts.WorkerTaskStatusCompleted {
			t.Fatalf("task %s expected Completed, got %s", id, task.Status)
		}
	}
}

func TestDispatcher_SkipsNonPending(t *testing.T) {
	pool := dispatcherTestPool(t)
	ctx := context.Background()

	runningID, _ := pool.Submit(ctx, contracts.WorkerTask{Type: "download"})
	pool.UpdateStatus(ctx, runningID, contracts.WorkerTaskStatusRunning, "")

	completedID, _ := pool.Submit(ctx, contracts.WorkerTask{Type: "download"})
	pool.UpdateStatus(ctx, completedID, contracts.WorkerTaskStatusCompleted, "")

	failedID, _ := pool.Submit(ctx, contracts.WorkerTask{Type: "download"})
	pool.UpdateStatus(ctx, failedID, contracts.WorkerTaskStatusFailed, "")

	reg := &mockRegistry{
		entries: map[string][]contracts.ModuleEntry{
			"executor.download": {
				{Info: contracts.ModuleInfo{ID: "downloader-test"}},
			},
		},
	}
	mockMesh := &mockMeshCaller{
		callFn: func(_ context.Context, _, _ string, _ []byte) ([]byte, error) {
			t.Fatal("Call should not be invoked for non-pending tasks")
			return nil, nil
		},
	}

	disp := NewDispatcher(pool, reg, mockMesh)
	disp.dispatchOnce(ctx)
}

func TestDispatcher_StartStop(t *testing.T) {
	pool := dispatcherTestPool(t)
	reg := &mockRegistry{entries: make(map[string][]contracts.ModuleEntry)}
	mockMesh := &mockMeshCaller{
		callFn: func(_ context.Context, _, _ string, _ []byte) ([]byte, error) {
			return nil, nil
		},
	}

	disp := NewDispatcher(pool, reg, mockMesh)
	disp.SetInterval(50 * time.Millisecond)

	ctx, cancel := context.WithCancel(context.Background())
	disp.Start(ctx)
	time.Sleep(100 * time.Millisecond)
	cancel()
	time.Sleep(50 * time.Millisecond)
}

func TestDispatcher_SetIntervalIgnoresNegative(t *testing.T) {
	disp := NewDispatcher(New("node-test"), nil, nil)
	disp.SetInterval(-1 * time.Second)
	if disp.interval != defaultDispatchInterval {
		t.Fatalf("expected default interval %s, got %s", defaultDispatchInterval, disp.interval)
	}
}

type mockNodeResolver map[string]string

func (m mockNodeResolver) NodeForModule(moduleID string) string {
	return m[moduleID]
}

func TestDispatcher_AssignedNodeFromResolver(t *testing.T) {
	pool := dispatcherTestPool(t)
	ctx := context.Background()

	id, err := pool.Submit(ctx, contracts.WorkerTask{
		Type:       "download",
		MaxRetries: 3,
	})
	if err != nil {
		t.Fatalf("Submit failed: %v", err)
	}

	reg := &mockRegistry{
		entries: map[string][]contracts.ModuleEntry{
			"executor.download": {
				{Info: contracts.ModuleInfo{ID: "downloader-test"}},
			},
		},
	}
	mockMesh := &mockMeshCaller{
		callFn: func(_ context.Context, targetModule, method string, _ []byte) ([]byte, error) {
			if targetModule != "downloader-test" {
				t.Fatalf("unexpected target %s", targetModule)
			}
			return []byte(`ok`), nil
		},
	}

	disp := NewDispatcher(pool, reg, mockMesh)
	disp.SetNodeResolver(mockNodeResolver{"downloader-test": "node-host-1"})
	disp.dispatchOnce(ctx)

	task, err := pool.Status(ctx, id)
	if err != nil {
		t.Fatalf("Status failed: %v", err)
	}
	if task.Status != contracts.WorkerTaskStatusCompleted {
		t.Fatalf("expected Completed, got %s", task.Status)
	}
	if task.AssignedNode != "node-host-1" {
		t.Errorf("expected AssignedNode node-host-1, got %q", task.AssignedNode)
	}
	if task.Meta == nil || task.Meta[metaExecutorKey] != "downloader-test" {
		t.Errorf("expected Meta[%q]=downloader-test, got %v", metaExecutorKey, task.Meta)
	}
}

func TestDispatcher_SkipsUnknownHost(t *testing.T) {
	pool := dispatcherTestPool(t)
	ctx := context.Background()

	id, err := pool.Submit(ctx, contracts.WorkerTask{Type: "download", MaxRetries: 3})
	if err != nil {
		t.Fatalf("Submit failed: %v", err)
	}

	reg := &mockRegistry{
		entries: map[string][]contracts.ModuleEntry{
			"executor.download": {
				{Info: contracts.ModuleInfo{ID: "dead-executor"}},
				{Info: contracts.ModuleInfo{ID: "live-executor"}},
			},
		},
	}
	var called string
	mockMesh := &mockMeshCaller{
		callFn: func(_ context.Context, targetModule, _ string, _ []byte) ([]byte, error) {
			called = targetModule
			return []byte(`ok`), nil
		},
	}

	disp := NewDispatcher(pool, reg, mockMesh)
	disp.SetNodeResolver(mockNodeResolver{"live-executor": "node-alive"})
	disp.dispatchOnce(ctx)

	if called != "live-executor" {
		t.Fatalf("expected live-executor, got %q", called)
	}
	task, _ := pool.Status(ctx, id)
	if task.AssignedNode != "node-alive" {
		t.Errorf("expected node-alive, got %q", task.AssignedNode)
	}
}

func TestDispatcher_AllHostsUnknownLeavesPending(t *testing.T) {
	pool := dispatcherTestPool(t)
	ctx := context.Background()

	id, err := pool.Submit(ctx, contracts.WorkerTask{Type: "download"})
	if err != nil {
		t.Fatalf("Submit failed: %v", err)
	}

	reg := &mockRegistry{
		entries: map[string][]contracts.ModuleEntry{
			"executor.download": {
				{Info: contracts.ModuleInfo{ID: "ghost-executor"}},
			},
		},
	}
	mockMesh := &mockMeshCaller{
		callFn: func(_ context.Context, _, _ string, _ []byte) ([]byte, error) {
			t.Fatal("Call should not run when host is unknown")
			return nil, nil
		},
	}

	disp := NewDispatcher(pool, reg, mockMesh)
	disp.SetNodeResolver(mockNodeResolver{})
	disp.dispatchOnce(ctx)

	task, _ := pool.Status(ctx, id)
	if task.Status != contracts.WorkerTaskStatusPending {
		t.Fatalf("expected Pending, got %s", task.Status)
	}
}

func TestClusterNodeResolver_NodeForModule(t *testing.T) {
	r := NewClusterNodeResolver(&stubCluster{
		members: []contracts.NodeInfo{
			{ID: "n1", ModuleIDs: []string{"mod-a"}},
			{ID: "n2", ModuleIDs: []string{"mod-b", "mod-c"}},
		},
	})
	if got := r.NodeForModule("mod-b"); got != "n2" {
		t.Errorf("expected n2, got %q", got)
	}
	if got := r.NodeForModule("missing"); got != "" {
		t.Errorf("expected empty, got %q", got)
	}
}

func TestClusterNodeResolver_PrefersLiveLocalNode(t *testing.T) {
	r := NewClusterNodeResolver(&stubCluster{
		local:   contracts.NodeInfo{ID: "self", ModuleIDs: []string{"late-mod"}},
		members: []contracts.NodeInfo{{ID: "self"}},
	})
	if got := r.NodeForModule("late-mod"); got != "self" {
		t.Errorf("expected self, got %q", got)
	}
}

type stubCluster struct {
	members []contracts.NodeInfo
	local   contracts.NodeInfo
}

func (s *stubCluster) Start(context.Context) error           { return nil }
func (s *stubCluster) Stop(context.Context) error            { return nil }
func (s *stubCluster) Members() []contracts.NodeInfo         { return s.members }
func (s *stubCluster) Leader() *contracts.NodeInfo           { return nil }
func (s *stubCluster) LocalNode() contracts.NodeInfo         { return s.local }
func (s *stubCluster) Events() <-chan contracts.ClusterEvent { return nil }
func (s *stubCluster) Health(context.Context) error          { return nil }
func (s *stubCluster) FindNodesByLabel(context.Context, string, string) []contracts.NodeInfo {
	return nil
}
func (s *stubCluster) FindNodesByModule(context.Context, string) []contracts.NodeInfo { return nil }
