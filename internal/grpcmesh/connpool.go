package grpcmesh

import (
	"log/slog"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/keepalive"
)

const (
	// connPoolIdleTimeout is how long an idle connection stays in the pool
	// before being evicted.
	connPoolIdleTimeout = 5 * time.Minute

	// connPoolCleanupInterval is how often the cleanup goroutine runs.
	connPoolCleanupInterval = 1 * time.Minute
)

// pooledConn wraps a gRPC ClientConn with its last-used timestamp for eviction.
type pooledConn struct {
	conn     *grpc.ClientConn
	lastUsed time.Time
}

// ConnPool caches gRPC connections keyed by target address.
// Connections are evicted after connPoolIdleTimeout of inactivity.
// Thread-safe.
//
// Usage:
//
//	pool := NewConnPool(dialOpts...)
//	conn, err := pool.Get("node.example.com:9090")
//	pool.Close() // on shutdown
type ConnPool struct {
	mu          sync.Mutex
	connections map[string]*pooledConn
	dialOpts    []grpc.DialOption
	stopCh      chan struct{}
}

// NewConnPool creates a connection pool with the given dial options
// (TLS credentials, etc.). A background goroutine periodically evicts
// idle or unhealthy connections.
//
// Keepalive pings are injected automatically to detect dead peers within
// 20 seconds so that stale pool entries are evicted on the next cleanup tick
// rather than waiting the full idle timeout.
func NewConnPool(dialOpts ...grpc.DialOption) *ConnPool {
	// Prepend keepalive params so callers can override if needed.
	kpOpts := []grpc.DialOption{
		grpc.WithKeepaliveParams(keepalive.ClientParameters{
			Time:                20 * time.Second, // send pings every 20s when idle
			Timeout:             10 * time.Second, // wait 10s for ping ack
			PermitWithoutStream: true,             // ping even without active RPCs
		}),
	}
	p := &ConnPool{
		connections: make(map[string]*pooledConn),
		dialOpts:    append(kpOpts, dialOpts...),
		stopCh:      make(chan struct{}),
	}
	go p.cleanupLoop()
	return p
}

// Get returns a gRPC connection for the given address.
// If a cached connection exists, it is reused. Otherwise a new connection
// is dialed and cached. The caller should NOT close the returned connection
// — the pool manages its lifecycle.
func (p *ConnPool) Get(addr string) (*grpc.ClientConn, error) {
	p.mu.Lock()
	pc, ok := p.connections[addr]
	if ok {
		pc.lastUsed = time.Now()
		p.mu.Unlock()
		return pc.conn, nil
	}
	p.mu.Unlock()

	conn, err := grpc.NewClient(addr, p.dialOpts...)
	if err != nil {
		return nil, err
	}

	p.mu.Lock()
	// Double-check: another goroutine may have created a connection
	// for the same address while we were dialing.
	if existing, ok := p.connections[addr]; ok {
		p.mu.Unlock()
		conn.Close() // discard our newly created connection
		return existing.conn, nil
	}

	p.connections[addr] = &pooledConn{
		conn:     conn,
		lastUsed: time.Now(),
	}
	p.mu.Unlock()

	slog.Debug("connpool: created new connection", "addr", addr)
	return conn, nil
}

// Close drains the pool, closing all cached connections.
func (p *ConnPool) Close() {
	close(p.stopCh)

	p.mu.Lock()
	defer p.mu.Unlock()

	for addr, pc := range p.connections {
		if err := pc.conn.Close(); err != nil {
			slog.Warn("connpool: error closing connection", "addr", addr, "error", err)
		}
	}
	p.connections = make(map[string]*pooledConn)
	slog.Info("connpool: all connections closed")
}

// Size returns the current number of cached connections.
func (p *ConnPool) Size() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.connections)
}

// cleanupLoop periodically evicts idle connections.
func (p *ConnPool) cleanupLoop() {
	ticker := time.NewTicker(connPoolCleanupInterval)
	defer ticker.Stop()

	for {
		select {
		case <-p.stopCh:
			return
		case <-ticker.C:
			p.evictIdle()
		}
	}
}

func (p *ConnPool) evictIdle() {
	p.mu.Lock()
	defer p.mu.Unlock()

	now := time.Now()
	for addr, pc := range p.connections {
		idle := now.Sub(pc.lastUsed) >= connPoolIdleTimeout

		// Also evict connections in a terminal or transient failure state.
		// connectivity.GetState is cheap and doesn't block.
		state := pc.conn.GetState()
		unhealthy := state == connectivity.TransientFailure || state == connectivity.Shutdown

		if idle || unhealthy {
			reason := "idle"
			if unhealthy {
				reason = state.String()
			}
			if err := pc.conn.Close(); err != nil {
				slog.Warn("connpool: error closing connection", "addr", addr, "reason", reason, "error", err)
			}
			delete(p.connections, addr)
			slog.Debug("connpool: evicted connection", "addr", addr, "reason", reason)
		}
	}
}
