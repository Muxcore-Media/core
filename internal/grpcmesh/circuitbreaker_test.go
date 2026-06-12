package grpcmesh

import (
	"sync"
	"testing"
	"time"
)

func TestNewNodeBreaker_InitialState(t *testing.T) {
	b := newNodeBreaker()
	if b.state != cbClosed {
		t.Fatalf("expected initial state cbClosed, got %d", b.state)
	}
	if b.failures != 0 {
		t.Fatalf("expected 0 failures, got %d", b.failures)
	}
	if b.threshold != defaultCBThreshold {
		t.Fatalf("expected threshold %d, got %d", defaultCBThreshold, b.threshold)
	}
	if b.cooldown != defaultCBCooldown {
		t.Fatalf("expected cooldown %v, got %v", defaultCBCooldown, b.cooldown)
	}
	if b.reset != defaultCBReset {
		t.Fatalf("expected reset %v, got %v", defaultCBReset, b.reset)
	}
}

func TestNodeBreaker_Allow_ClosedState(t *testing.T) {
	b := newNodeBreaker()
	if !b.allow() {
		t.Fatal("closed breaker should allow calls")
	}
}

func TestNodeBreaker_Allow_OpenState(t *testing.T) {
	b := newNodeBreaker()
	b.mu.Lock()
	b.state = cbOpen
	b.openedAt = time.Now()
	b.mu.Unlock()

	if b.allow() {
		t.Fatal("open breaker should reject calls")
	}
}

func TestNodeBreaker_Allow_HalfOpenState(t *testing.T) {
	b := newNodeBreaker()
	b.mu.Lock()
	b.state = cbHalfOpen
	b.mu.Unlock()

	if !b.allow() {
		t.Fatal("half-open breaker should allow calls")
	}
}

func TestNodeBreaker_RecordFailure_ClosedToOpen(t *testing.T) {
	b := newNodeBreaker()
	for i := 0; i < b.threshold-1; i++ {
		b.recordFailure()
	}
	b.mu.Lock()
	if b.state != cbClosed {
		t.Fatalf("expected still closed after %d failures, got %d", b.threshold-1, b.state)
	}
	b.mu.Unlock()

	b.recordFailure()
	b.mu.Lock()
	if b.state != cbOpen {
		t.Fatalf("expected open after %d failures, got %d", b.threshold, b.state)
	}
	if b.openedAt.IsZero() {
		t.Fatal("openedAt should be set when transitioning to open")
	}
	b.mu.Unlock()
}

func TestNodeBreaker_RecordFailure_HalfOpenToOpen(t *testing.T) {
	b := newNodeBreaker()
	b.mu.Lock()
	b.state = cbHalfOpen
	b.mu.Unlock()

	b.recordFailure()
	b.mu.Lock()
	if b.state != cbOpen {
		t.Fatalf("expected open after failure in half-open, got %d", b.state)
	}
	if b.openedAt.IsZero() {
		t.Fatal("openedAt should be set on half-open failure")
	}
	b.mu.Unlock()
}

func TestNodeBreaker_RecordSuccess_ResetsFailures(t *testing.T) {
	b := newNodeBreaker()
	for i := 0; i < 3; i++ {
		b.recordFailure()
	}
	b.recordSuccess()

	b.mu.Lock()
	if b.failures != 0 {
		t.Fatalf("expected 0 failures after success, got %d", b.failures)
	}
	if b.state != cbClosed {
		t.Fatalf("expected closed state, got %d", b.state)
	}
	b.mu.Unlock()
}

func TestNodeBreaker_RecordSuccess_HalfOpenToClosed(t *testing.T) {
	b := newNodeBreaker()
	b.mu.Lock()
	b.state = cbHalfOpen
	b.mu.Unlock()

	b.recordSuccess()
	b.mu.Lock()
	if b.state != cbClosed {
		t.Fatalf("expected closed after success in half-open, got %d", b.state)
	}
	if b.failures != 0 {
		t.Fatalf("expected 0 failures, got %d", b.failures)
	}
	b.mu.Unlock()
}

func TestNodeBreaker_Allow_OpenToHalfOpenAfterReset(t *testing.T) {
	b := newNodeBreaker()
	b.mu.Lock()
	b.state = cbOpen
	b.openedAt = time.Now().Add(-b.reset - time.Second)
	b.mu.Unlock()

	if !b.allow() {
		t.Fatal("breaker should transition to half-open and allow after reset duration")
	}
	b.mu.Lock()
	if b.state != cbHalfOpen {
		t.Fatalf("expected half-open after reset, got %d", b.state)
	}
	b.mu.Unlock()
}

func TestNodeBreaker_Allow_OpenNotYetExpired(t *testing.T) {
	b := newNodeBreaker()
	b.mu.Lock()
	b.state = cbOpen
	b.openedAt = time.Now()
	b.mu.Unlock()

	if b.allow() {
		t.Fatal("breaker should still be open before reset duration expires")
	}
}

func TestCircuitBreakerSet_Allow_CreatesBreaker(t *testing.T) {
	s := NewCircuitBreakerSet()
	if !s.Allow("node-1") {
		t.Fatal("new breaker should allow")
	}
	s.mu.Lock()
	if _, ok := s.breakers["node-1"]; !ok {
		t.Fatal("expected breaker to be created for node-1")
	}
	s.mu.Unlock()
}

func TestCircuitBreakerSet_Allow_ReturnsSameBreaker(t *testing.T) {
	s := NewCircuitBreakerSet()
	s.Allow("node-1")
	s.mu.Lock()
	first := s.breakers["node-1"]
	s.mu.Unlock()

	s.Allow("node-1")
	s.mu.Lock()
	second := s.breakers["node-1"]
	s.mu.Unlock()

	if first != second {
		t.Fatal("expected same breaker instance on repeated access")
	}
}

func TestCircuitBreakerSet_RecordFailure_Existing(t *testing.T) {
	s := NewCircuitBreakerSet()
	s.Allow("node-1")
	s.RecordFailure("node-1")

	s.mu.Lock()
	b := s.breakers["node-1"]
	s.mu.Unlock()

	b.mu.Lock()
	if b.failures != 1 {
		t.Fatalf("expected 1 failure, got %d", b.failures)
	}
	b.mu.Unlock()
}

func TestCircuitBreakerSet_RecordFailure_NewNode(t *testing.T) {
	s := NewCircuitBreakerSet()
	s.RecordFailure("node-new")

	s.mu.Lock()
	b, ok := s.breakers["node-new"]
	if !ok {
		t.Fatal("expected breaker to be created for new node")
	}
	s.mu.Unlock()

	b.mu.Lock()
	if b.failures != 1 {
		t.Fatalf("expected 1 failure, got %d", b.failures)
	}
	b.mu.Unlock()
}

func TestCircuitBreakerSet_RecordSuccess_Existing(t *testing.T) {
	s := NewCircuitBreakerSet()
	s.RecordFailure("node-1")
	s.RecordFailure("node-1")
	s.RecordSuccess("node-1")

	s.mu.Lock()
	b := s.breakers["node-1"]
	s.mu.Unlock()

	b.mu.Lock()
	if b.failures != 0 {
		t.Fatalf("expected 0 failures after success, got %d", b.failures)
	}
	b.mu.Unlock()
}

func TestCircuitBreakerSet_RecordSuccess_NewNode(t *testing.T) {
	s := NewCircuitBreakerSet()
	s.RecordSuccess("node-x")

	s.mu.Lock()
	b, ok := s.breakers["node-x"]
	if !ok {
		t.Fatal("expected breaker to be created for new node")
	}
	s.mu.Unlock()

	b.mu.Lock()
	if b.failures != 0 {
		t.Fatalf("expected 0 failures, got %d", b.failures)
	}
	b.mu.Unlock()
}

func TestCircuitBreakerSet_FullLifecycle(t *testing.T) {
	s := NewCircuitBreakerSet()
	node := "node-lifecycle"

	for i := 0; i < defaultCBThreshold; i++ {
		if !s.Allow(node) {
			t.Fatalf("should allow on attempt %d", i)
		}
		s.RecordFailure(node)
	}

	if s.Allow(node) {
		t.Fatal("breaker should be open after reaching threshold")
	}

	s.mu.Lock()
	b := s.breakers[node]
	s.mu.Unlock()

	b.mu.Lock()
	b.openedAt = time.Now().Add(-b.reset - time.Second)
	b.mu.Unlock()

	if !s.Allow(node) {
		t.Fatal("should allow after reset duration (half-open)")
	}

	s.RecordSuccess(node)

	if !s.Allow(node) {
		t.Fatal("should allow after recovery (closed)")
	}
}

func TestCircuitBreakerSet_ConcurrentAccess(t *testing.T) {
	s := NewCircuitBreakerSet()
	const goroutines = 50
	const iterations = 200

	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func(id int) {
			defer wg.Done()
			node := "node-concurrent"
			for j := 0; j < iterations; j++ {
				s.Allow(node)
				if j%3 == 0 {
					s.RecordFailure(node)
				} else if j%3 == 1 {
					s.RecordSuccess(node)
				}
			}
		}(i)
	}
	wg.Wait()
}

func TestCircuitBreakerSet_ConcurrentMultipleNodes(t *testing.T) {
	s := NewCircuitBreakerSet()
	const goroutines = 20
	const iterations = 100

	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func(id int) {
			defer wg.Done()
			node := "node"
			if id%2 == 0 {
				node = "node-even"
			} else {
				node = "node-odd"
			}
			for j := 0; j < iterations; j++ {
				s.Allow(node)
				s.RecordFailure(node)
				s.RecordSuccess(node)
			}
		}(i)
	}
	wg.Wait()
}
