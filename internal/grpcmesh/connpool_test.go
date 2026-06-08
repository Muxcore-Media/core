package grpcmesh

import (
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func insecureOpts() []grpc.DialOption {
	return []grpc.DialOption{grpc.WithTransportCredentials(insecure.NewCredentials())}
}

func TestConnPool_GetCreatesConnection(t *testing.T) {
	// Use a non-existent address — we're testing pool mechanics, not actual connectivity.
	// grpc.NewClient doesn't dial eagerly, so this won't fail.
	pool := NewConnPool(insecureOpts()...)
	defer pool.Close()

	conn, err := pool.Get("localhost:19999")
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if conn == nil {
		t.Fatal("expected non-nil conn")
	}
	if pool.Size() != 1 {
		t.Errorf("expected pool size 1, got %d", pool.Size())
	}
}

func TestConnPool_GetReusesConnection(t *testing.T) {
	pool := NewConnPool(insecureOpts()...)
	defer pool.Close()

	conn1, _ := pool.Get("localhost:19999")
	conn2, _ := pool.Get("localhost:19999")

	if conn1 != conn2 {
		t.Error("expected same connection to be returned on second Get")
	}
	if pool.Size() != 1 {
		t.Errorf("expected pool size 1 after two Gets for same addr, got %d", pool.Size())
	}
}

func TestConnPool_DifferentAddresses(t *testing.T) {
	pool := NewConnPool(insecureOpts()...)
	defer pool.Close()

	pool.Get("localhost:19991")
	pool.Get("localhost:19992")

	if pool.Size() != 2 {
		t.Errorf("expected pool size 2 for two different addrs, got %d", pool.Size())
	}
}

func TestConnPool_Close(t *testing.T) {
	pool := NewConnPool(insecureOpts()...)

	pool.Get("localhost:19999")
	if pool.Size() != 1 {
		t.Fatalf("expected 1 conn before Close")
	}

	pool.Close()
	if pool.Size() != 0 {
		t.Errorf("expected pool empty after Close, got %d", pool.Size())
	}
}

func TestConnPool_EvictIdle(t *testing.T) {
	pool := NewConnPool(insecureOpts()...)
	defer pool.Close()

	pool.Get("localhost:19999")

	// Manually mark the connection as very old.
	pool.mu.Lock()
	for _, pc := range pool.connections {
		pc.lastUsed = time.Now().Add(-10 * time.Minute)
	}
	pool.mu.Unlock()

	pool.evictIdle()

	if pool.Size() != 0 {
		t.Errorf("expected idle connection evicted, pool size = %d", pool.Size())
	}
}

func TestConnPool_ConcurrentGet(t *testing.T) {
	pool := NewConnPool(insecureOpts()...)
	defer pool.Close()

	const goroutines = 20
	var wg sync.WaitGroup
	errs := make(chan error, goroutines)

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := pool.Get("localhost:19999"); err != nil {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)

	for err := range errs {
		t.Errorf("concurrent Get error: %v", err)
	}

	// All goroutines should have gotten the same connection.
	if pool.Size() != 1 {
		t.Errorf("expected pool size 1 after concurrent Gets, got %d", pool.Size())
	}
}
