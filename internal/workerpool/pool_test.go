package workerpool

import (
	"context"
	"testing"
	"time"

	"github.com/Muxcore-Media/core/pkg/contracts"
)

func TestSubmit_GeneratesID(t *testing.T) {
	p := New("node-a")
	id, err := p.Submit(context.Background(), contracts.WorkerTask{
		Type:    "download",
		Payload: []byte("hello"),
	})
	if err != nil {
		t.Fatalf("Submit failed: %v", err)
	}
	if id == "" {
		t.Fatal("expected non-empty task ID")
	}
}

func TestSubmit_PreservesID(t *testing.T) {
	p := New("node-a")
	id, err := p.Submit(context.Background(), contracts.WorkerTask{
		ID:   "my-task-1",
		Type: "transcode",
	})
	if err != nil {
		t.Fatalf("Submit failed: %v", err)
	}
	if id != "my-task-1" {
		t.Fatalf("expected my-task-1, got %s", id)
	}
}

func TestSubmit_RequiresType(t *testing.T) {
	p := New("node-a")
	_, err := p.Submit(context.Background(), contracts.WorkerTask{})
	if err == nil {
		t.Fatal("expected error for empty task type")
	}
}

func TestStatus_Found(t *testing.T) {
	p := New("node-a")
	id, _ := p.Submit(context.Background(), contracts.WorkerTask{Type: "download"})
	task, err := p.Status(context.Background(), id)
	if err != nil {
		t.Fatalf("Status failed: %v", err)
	}
	if task.Type != "download" {
		t.Errorf("expected type download, got %s", task.Type)
	}
	if task.Status != contracts.WorkerTaskStatusPending {
		t.Errorf("expected status pending, got %s", task.Status)
	}
}

func TestStatus_NotFound(t *testing.T) {
	p := New("node-a")
	_, err := p.Status(context.Background(), "nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent task")
	}
}

func TestCancel(t *testing.T) {
	p := New("node-a")
	id, _ := p.Submit(context.Background(), contracts.WorkerTask{Type: "download"})

	if err := p.Cancel(context.Background(), id); err != nil {
		t.Fatalf("Cancel failed: %v", err)
	}

	task, _ := p.Status(context.Background(), id)
	if task.Status != contracts.WorkerTaskStatusCancelled {
		t.Errorf("expected cancelled, got %s", task.Status)
	}
}

func TestCancel_Completed(t *testing.T) {
	p := New("node-a")
	id, _ := p.Submit(context.Background(), contracts.WorkerTask{Type: "download"})
	p.UpdateStatus(context.Background(), id, contracts.WorkerTaskStatusCompleted, "")

	err := p.Cancel(context.Background(), id)
	if err == nil {
		t.Fatal("expected error cancelling completed task")
	}
}

func TestCancel_NotFound(t *testing.T) {
	p := New("node-a")
	err := p.Cancel(context.Background(), "nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent task")
	}
}

func TestList_All(t *testing.T) {
	p := New("node-a")
	p.Submit(context.Background(), contracts.WorkerTask{Type: "download"})
	p.Submit(context.Background(), contracts.WorkerTask{Type: "transcode"})

	tasks, err := p.List(context.Background(), nil)
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	if len(tasks) != 2 {
		t.Errorf("expected 2 tasks, got %d", len(tasks))
	}
}

func TestList_FilterByStatus(t *testing.T) {
	p := New("node-a")
	id1, _ := p.Submit(context.Background(), contracts.WorkerTask{Type: "download"})
	p.Submit(context.Background(), contracts.WorkerTask{Type: "transcode"})
	p.Cancel(context.Background(), id1)

	tasks, err := p.List(context.Background(), &contracts.WorkerTaskFilter{
		Status: contracts.WorkerTaskStatusCancelled,
	})
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	if len(tasks) != 1 {
		t.Errorf("expected 1 cancelled task, got %d", len(tasks))
	}
}

func TestList_FilterByType(t *testing.T) {
	p := New("node-a")
	p.Submit(context.Background(), contracts.WorkerTask{Type: "download"})
	p.Submit(context.Background(), contracts.WorkerTask{Type: "transcode"})

	tasks, err := p.List(context.Background(), &contracts.WorkerTaskFilter{
		Type: "download",
	})
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	if len(tasks) != 1 {
		t.Errorf("expected 1 download task, got %d", len(tasks))
	}
}

func TestList_FilterByNode(t *testing.T) {
	p := New("node-a")
	p.Submit(context.Background(), contracts.WorkerTask{
		Type:         "download",
		AssignedNode: "node-b",
	})
	p.Submit(context.Background(), contracts.WorkerTask{
		Type:         "transcode",
		AssignedNode: "node-c",
	})

	tasks, err := p.List(context.Background(), &contracts.WorkerTaskFilter{
		AssignedNode: "node-b",
	})
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	if len(tasks) != 1 {
		t.Errorf("expected 1 task for node-b, got %d", len(tasks))
	}
}

func TestList_Empty(t *testing.T) {
	p := New("node-a")
	tasks, err := p.List(context.Background(), nil)
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	if len(tasks) != 0 {
		t.Errorf("expected 0 tasks, got %d", len(tasks))
	}
}

func TestReassign_Running(t *testing.T) {
	p := New("node-a")
	id, _ := p.Submit(context.Background(), contracts.WorkerTask{
		Type:         "download",
		AssignedNode: "node-b",
	})
	p.UpdateStatus(context.Background(), id, contracts.WorkerTaskStatusRunning, "")

	if err := p.Reassign(context.Background(), id, "node-c"); err != nil {
		t.Fatalf("Reassign failed: %v", err)
	}

	task, _ := p.Status(context.Background(), id)
	if task.AssignedNode != "node-c" {
		t.Errorf("expected node-c, got %s", task.AssignedNode)
	}
	if task.Status != contracts.WorkerTaskStatusAssigned {
		t.Errorf("expected assigned after reassign, got %s", task.Status)
	}
}

func TestReassign_Pending(t *testing.T) {
	p := New("node-a")
	id, _ := p.Submit(context.Background(), contracts.WorkerTask{
		Type: "download",
	})

	if err := p.Reassign(context.Background(), id, "node-d"); err != nil {
		t.Fatalf("Reassign failed: %v", err)
	}

	task, _ := p.Status(context.Background(), id)
	if task.AssignedNode != "node-d" {
		t.Errorf("expected node-d, got %s", task.AssignedNode)
	}
	if task.Status != contracts.WorkerTaskStatusAssigned {
		t.Errorf("expected assigned after reassign, got %s", task.Status)
	}
}

func TestReassign_Completed(t *testing.T) {
	p := New("node-a")
	id, _ := p.Submit(context.Background(), contracts.WorkerTask{
		Type:         "download",
		AssignedNode: "node-b",
	})
	p.UpdateStatus(context.Background(), id, contracts.WorkerTaskStatusCompleted, "")

	err := p.Reassign(context.Background(), id, "node-c")
	if err == nil {
		t.Fatal("expected error reassigning completed task")
	}
}

func TestReassign_EmptyNode(t *testing.T) {
	p := New("node-a")
	id, _ := p.Submit(context.Background(), contracts.WorkerTask{
		Type: "download",
	})
	err := p.Reassign(context.Background(), id, "")
	if err == nil {
		t.Fatal("expected error for empty node")
	}
}

func TestReassign_NotFound(t *testing.T) {
	p := New("node-a")
	err := p.Reassign(context.Background(), "nonexistent", "node-b")
	if err == nil {
		t.Fatal("expected error for nonexistent task")
	}
}

func TestReassign_MaxRetries(t *testing.T) {
	p := New("node-a")
	id, _ := p.Submit(context.Background(), contracts.WorkerTask{
		Type:         "download",
		MaxRetries:   2,
		AssignedNode: "node-b",
	})
	p.UpdateStatus(context.Background(), id, contracts.WorkerTaskStatusRunning, "")

	// First reassign
	p.Reassign(context.Background(), id, "node-c")
	task1, _ := p.Status(context.Background(), id)
	if task1.RetryCount != 1 {
		t.Errorf("expected retry count 1, got %d", task1.RetryCount)
	}
	if task1.Status != contracts.WorkerTaskStatusAssigned {
		t.Errorf("expected assigned, got %s", task1.Status)
	}

	// Second reassign (within retries)
	p.UpdateStatus(context.Background(), id, contracts.WorkerTaskStatusRunning, "")
	p.Reassign(context.Background(), id, "node-d")
	task2, _ := p.Status(context.Background(), id)
	if task2.RetryCount != 2 {
		t.Errorf("expected retry count 2, got %d", task2.RetryCount)
	}

	// Third reassign (exceeds MaxRetries)
	p.UpdateStatus(context.Background(), id, contracts.WorkerTaskStatusRunning, "")
	p.Reassign(context.Background(), id, "node-e")
	task3, _ := p.Status(context.Background(), id)
	if task3.Status != contracts.WorkerTaskStatusFailed {
		t.Errorf("expected failed after max retries, got %s", task3.Status)
	}
}

func TestUpdateStatus(t *testing.T) {
	p := New("node-a")
	id, _ := p.Submit(context.Background(), contracts.WorkerTask{
		Type:         "download",
		AssignedNode: "node-b",
	})

	if err := p.UpdateStatus(context.Background(), id, contracts.WorkerTaskStatusRunning, ""); err != nil {
		t.Fatalf("UpdateStatus failed: %v", err)
	}

	task, _ := p.Status(context.Background(), id)
	if task.Status != contracts.WorkerTaskStatusRunning {
		t.Errorf("expected running, got %s", task.Status)
	}
	if task.StartedAt.IsZero() {
		t.Error("expected StartedAt to be set")
	}
}

func TestUpdateStatus_Completed(t *testing.T) {
	p := New("node-a")
	id, _ := p.Submit(context.Background(), contracts.WorkerTask{
		Type: "download",
	})

	p.UpdateStatus(context.Background(), id, contracts.WorkerTaskStatusCompleted, "")
	task, _ := p.Status(context.Background(), id)
	if task.CompletedAt.IsZero() {
		t.Error("expected CompletedAt to be set")
	}
}

func TestUpdateStatus_Failed(t *testing.T) {
	p := New("node-a")
	id, _ := p.Submit(context.Background(), contracts.WorkerTask{
		Type: "download",
	})

	err := p.UpdateStatus(context.Background(), id, contracts.WorkerTaskStatusFailed, "connection timeout")
	if err != nil {
		t.Fatalf("UpdateStatus failed: %v", err)
	}

	task, _ := p.Status(context.Background(), id)
	if task.Status != contracts.WorkerTaskStatusFailed {
		t.Errorf("expected failed, got %s", task.Status)
	}
	if task.Error != "connection timeout" {
		t.Errorf("expected 'connection timeout', got %q", task.Error)
	}
}

func TestHeartbeat_OK(t *testing.T) {
	p := New("node-a")
	id, _ := p.Submit(context.Background(), contracts.WorkerTask{
		Type: "download",
	})
	p.UpdateStatus(context.Background(), id, contracts.WorkerTaskStatusRunning, "")

	if err := p.Heartbeat(context.Background(), id); err != nil {
		t.Fatalf("Heartbeat failed: %v", err)
	}

	task, _ := p.Status(context.Background(), id)
	if task.LastHeartbeat.IsZero() {
		t.Error("expected LastHeartbeat to be set")
	}
}

func TestHeartbeat_NotRunning(t *testing.T) {
	p := New("node-a")
	id, _ := p.Submit(context.Background(), contracts.WorkerTask{
		Type: "download",
	})

	err := p.Heartbeat(context.Background(), id)
	if err == nil {
		t.Fatal("expected error heartbeating non-running task")
	}
}

func TestHeartbeat_NotFound(t *testing.T) {
	p := New("node-a")
	err := p.Heartbeat(context.Background(), "nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent task")
	}
}

func TestCount(t *testing.T) {
	p := New("node-a")
	if c := p.Count(); c != 0 {
		t.Errorf("expected 0, got %d", c)
	}
	p.Submit(context.Background(), contracts.WorkerTask{Type: "a"})
	p.Submit(context.Background(), contracts.WorkerTask{Type: "b"})
	if c := p.Count(); c != 2 {
		t.Errorf("expected 2, got %d", c)
	}
}

func TestConcurrentAccess(t *testing.T) {
	p := New("node-a")
	done := make(chan struct{})
	go func() {
		for i := 0; i < 100; i++ {
			p.Submit(context.Background(), contracts.WorkerTask{Type: "download"})
		}
		done <- struct{}{}
	}()
	go func() {
		for i := 0; i < 100; i++ {
			p.List(context.Background(), nil)
		}
		done <- struct{}{}
	}()
	<-done
	<-done
	if c := p.Count(); c != 100 {
		t.Errorf("expected 100 tasks, got %d", c)
	}
}

func TestFailNodeTasks_Running(t *testing.T) {
	p := New("node-a")
	id, _ := p.Submit(context.Background(), contracts.WorkerTask{
		Type:         "download",
		AssignedNode: "node-b",
	})
	p.UpdateStatus(context.Background(), id, contracts.WorkerTaskStatusRunning, "")

	count := p.FailNodeTasks(context.Background(), "node-b")
	if count != 1 {
		t.Fatalf("expected 1 failed task, got %d", count)
	}

	task, _ := p.Status(context.Background(), id)
	if task.Status != contracts.WorkerTaskStatusFailed {
		t.Errorf("expected failed, got %s", task.Status)
	}
	if task.Error == "" {
		t.Error("expected non-empty error message")
	}
}

func TestFailNodeTasks_Assigned(t *testing.T) {
	p := New("node-a")
	id, _ := p.Submit(context.Background(), contracts.WorkerTask{
		Type:         "download",
		AssignedNode: "node-b",
	})

	count := p.FailNodeTasks(context.Background(), "node-b")
	if count != 1 {
		t.Fatalf("expected 1 failed task, got %d", count)
	}

	task, _ := p.Status(context.Background(), id)
	if task.Status != contracts.WorkerTaskStatusFailed {
		t.Errorf("expected failed, got %s", task.Status)
	}
}

func TestFailNodeTasks_SkipsCompleted(t *testing.T) {
	p := New("node-a")
	id, _ := p.Submit(context.Background(), contracts.WorkerTask{
		Type:         "download",
		AssignedNode: "node-b",
	})
	p.UpdateStatus(context.Background(), id, contracts.WorkerTaskStatusCompleted, "")

	count := p.FailNodeTasks(context.Background(), "node-b")
	if count != 0 {
		t.Errorf("expected 0 failed tasks, got %d", count)
	}
}

func TestFailNodeTasks_SkipsOtherNode(t *testing.T) {
	p := New("node-a")
	p.Submit(context.Background(), contracts.WorkerTask{
		Type:         "download",
		AssignedNode: "node-b",
	})

	count := p.FailNodeTasks(context.Background(), "node-c")
	if count != 0 {
		t.Errorf("expected 0 failed tasks for unrelated node, got %d", count)
	}
}

func TestFailNodeTasks_Empty(t *testing.T) {
	p := New("node-a")
	count := p.FailNodeTasks(context.Background(), "nonexistent")
	if count != 0 {
		t.Errorf("expected 0, got %d", count)
	}
}

func TestSetHeartbeatTimeout(t *testing.T) {
	p := New("node-a")
	p.SetHeartbeatTimeout(10 * time.Second)
	if p.heartbeatTimeout != 10*time.Second {
		t.Errorf("expected 10s, got %v", p.heartbeatTimeout)
	}
}

func TestReapStaleTasks_Healthy(t *testing.T) {
	p := New("node-a")
	p.SetHeartbeatTimeout(30 * time.Second)
	id, _ := p.Submit(context.Background(), contracts.WorkerTask{
		Type: "download",
	})
	p.UpdateStatus(context.Background(), id, contracts.WorkerTaskStatusRunning, "")
	p.Heartbeat(context.Background(), id)

	p.reapStaleTasks(context.Background(), )

	task, _ := p.Status(context.Background(), id)
	if task.Status != contracts.WorkerTaskStatusRunning {
		t.Errorf("expected still running, got %s", task.Status)
	}
}

func TestReapStaleTasks_Stale(t *testing.T) {
	p := New("node-a")
	p.SetHeartbeatTimeout(1 * time.Millisecond)
	id, _ := p.Submit(context.Background(), contracts.WorkerTask{
		Type: "download",
	})
	p.UpdateStatus(context.Background(), id, contracts.WorkerTaskStatusRunning, "")
	p.Heartbeat(context.Background(), id)

	// Wait for the heartbeat to go stale.
	time.Sleep(5 * time.Millisecond)

	p.reapStaleTasks(context.Background(), )

	task, _ := p.Status(context.Background(), id)
	if task.Status != contracts.WorkerTaskStatusFailed {
		t.Errorf("expected failed, got %s", task.Status)
	}
	if task.Error != "heartbeat timeout" {
		t.Errorf("expected 'heartbeat timeout', got %q", task.Error)
	}
}

func TestReapStaleTasks_SkipsPending(t *testing.T) {
	p := New("node-a")
	p.SetHeartbeatTimeout(1 * time.Millisecond)
	id, _ := p.Submit(context.Background(), contracts.WorkerTask{
		Type: "download",
	})
	time.Sleep(5 * time.Millisecond)

	p.reapStaleTasks(context.Background(), )

	task, _ := p.Status(context.Background(), id)
	if task.Status != contracts.WorkerTaskStatusPending {
		t.Errorf("expected still pending, got %s", task.Status)
	}
}

func TestReapStaleTasks_Multiple(t *testing.T) {
	p := New("node-a")
	p.SetHeartbeatTimeout(1 * time.Millisecond)
	id1, _ := p.Submit(context.Background(), contracts.WorkerTask{Type: "a"})
	id2, _ := p.Submit(context.Background(), contracts.WorkerTask{Type: "b"})
	id3, _ := p.Submit(context.Background(), contracts.WorkerTask{Type: "c"})
	p.UpdateStatus(context.Background(), id1, contracts.WorkerTaskStatusRunning, "")
	p.UpdateStatus(context.Background(), id2, contracts.WorkerTaskStatusRunning, "")
	// id3 stays pending

	time.Sleep(5 * time.Millisecond)
	p.reapStaleTasks(context.Background(), )

	t1, _ := p.Status(context.Background(), id1)
	t2, _ := p.Status(context.Background(), id2)
	t3, _ := p.Status(context.Background(), id3)
	if t1.Status != contracts.WorkerTaskStatusFailed {
		t.Errorf("task 1 expected failed, got %s", t1.Status)
	}
	if t2.Status != contracts.WorkerTaskStatusFailed {
		t.Errorf("task 2 expected failed, got %s", t2.Status)
	}
	if t3.Status != contracts.WorkerTaskStatusPending {
		t.Errorf("task 3 expected pending, got %s", t3.Status)
	}
}

func TestStartStop_NoPanic(t *testing.T) {
	p := New("node-a")
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancelled immediately — reaper should exit cleanly
	p.Start(ctx)
	p.Stop()
	// Calling Stop again should be safe.
	p.Stop()
}

func TestStart_DoubleStart(t *testing.T) {
	p := New("node-a")
	ctx := context.Background()
	p.Start(ctx)
	p.Start(ctx) // should not panic or deadlock
	p.Stop()
}
