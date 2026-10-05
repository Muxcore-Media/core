package main

import (
	"sync"
	"testing"
	"time"
)

// fakeGRPCServer's GracefulStop blocks until Stop is called, like a server
// with a parked streaming RPC.
type fakeGRPCServer struct {
	once    sync.Once
	stopped chan struct{}
}

func (f *fakeGRPCServer) GracefulStop() { <-f.stopped }
func (f *fakeGRPCServer) Stop()         { f.once.Do(func() { close(f.stopped) }) }

func TestGracefulStopGRPCForcesStopAfterTimeout(t *testing.T) {
	f := &fakeGRPCServer{stopped: make(chan struct{})}
	done := make(chan struct{})
	go func() { gracefulStopGRPC(f, 50*time.Millisecond); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("gracefulStopGRPC hung on a server whose GracefulStop never returns")
	}
}

type quickServer struct{ stops int }

func (q *quickServer) GracefulStop() {}
func (q *quickServer) Stop()         { q.stops++ }

func TestGracefulStopGRPCNoForceWhenDrained(t *testing.T) {
	q := &quickServer{}
	gracefulStopGRPC(q, time.Second)
	if q.stops != 0 {
		t.Fatalf("Stop called %d times for a server that drained", q.stops)
	}
}
