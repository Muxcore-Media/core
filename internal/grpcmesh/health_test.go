package grpcmesh

import (
	"context"
	"errors"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/Muxcore-Media/core/pkg/contracts"
	healthv1 "github.com/Muxcore-Media/core/proto/gen/muxcore/health/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
)

// --- Stubs ---

// stubRegistry implements contracts.Registry with controllable entries.
type stubHealthRegistry struct {
	entries map[string]contracts.ModuleEntry
	mu      sync.RWMutex
}

func newStubHealthRegistry() *stubHealthRegistry {
	return &stubHealthRegistry{entries: make(map[string]contracts.ModuleEntry)}
}

func (r *stubHealthRegistry) add(id string, mod contracts.Module) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.entries[id] = contracts.ModuleEntry{
		Info:   contracts.ModuleInfo{ID: id, Name: id},
		Module: mod,
	}
}

func (r *stubHealthRegistry) FindByRole(_ string) []contracts.ModuleEntry {
	return nil
}
func (r *stubHealthRegistry) FindByCapability(_ string) []contracts.ModuleEntry {
	return nil
}
func (r *stubHealthRegistry) SupportsCapability(_, _ string) bool {
	return false
}
func (r *stubHealthRegistry) Resolve(id string) (contracts.ModuleEntry, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	entry, ok := r.entries[id]
	if !ok {
		return contracts.ModuleEntry{}, errors.New("not found")
	}
	return entry, nil
}
func (r *stubHealthRegistry) ListAll() []contracts.ModuleEntry {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]contracts.ModuleEntry, 0, len(r.entries))
	for _, e := range r.entries {
		out = append(out, e)
	}
	return out
}
func (r *stubHealthRegistry) StartupOrder() ([]string, error) {
	return nil, nil
}
func (r *stubHealthRegistry) DependencyGraph(_ string) ([]string, error) {
	return nil, nil
}

// stubModule implements contracts.Module for health testing.
type stubHealthModule struct {
	healthErr error
}

func (m *stubHealthModule) Info() contracts.ModuleInfo {
	return contracts.ModuleInfo{ID: "test", Name: "test"}
}
func (m *stubHealthModule) Init(_ context.Context) error   { return nil }
func (m *stubHealthModule) Start(_ context.Context) error  { return nil }
func (m *stubHealthModule) Stop(_ context.Context) error   { return nil }
func (m *stubHealthModule) Health(_ context.Context) error { return m.healthErr }

// fakeHealthWatchServer implements healthv1.HealthService_WatchServer in-process.
type fakeHealthWatchServer struct {
	ctx      context.Context
	sent     []*healthv1.HealthCheckResponse
	mu       sync.Mutex
	sendHook func() // optional; called before each Send to inject delays
}

func (f *fakeHealthWatchServer) Send(resp *healthv1.HealthCheckResponse) error {
	f.mu.Lock()
	f.sent = append(f.sent, resp)
	f.mu.Unlock()
	if f.sendHook != nil {
		f.sendHook()
	}
	return nil
}
func (f *fakeHealthWatchServer) Context() context.Context     { return f.ctx }
func (f *fakeHealthWatchServer) SetHeader(metadata.MD) error  { return nil }
func (f *fakeHealthWatchServer) SendHeader(metadata.MD) error { return nil }
func (f *fakeHealthWatchServer) SetTrailer(metadata.MD)       {}
func (f *fakeHealthWatchServer) SendMsg(any) error            { return nil }
func (f *fakeHealthWatchServer) RecvMsg(any) error            { return nil }

// --- Tests ---

func TestNewHealthServer(t *testing.T) {
	reg := newStubHealthRegistry()
	srv := NewHealthServer(reg)
	if srv == nil {
		t.Fatal("expected non-nil health server")
	}
	if srv.reg != reg {
		t.Error("expected registry to be set")
	}
}

func TestHealthServer_Check_ModuleHealthy(t *testing.T) {
	reg := newStubHealthRegistry()
	reg.add("mod-a", &stubHealthModule{})
	srv := NewHealthServer(reg)

	resp, err := srv.Check(context.Background(), &healthv1.HealthCheckRequest{ModuleId: "mod-a"})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if resp.GetStatus() != healthv1.HealthCheckResponse_STATUS_HEALTHY {
		t.Errorf("expected HEALTHY, got %s", resp.GetStatus())
	}
}

func TestHealthServer_Check_ModuleDegraded(t *testing.T) {
	reg := newStubHealthRegistry()
	reg.add("mod-b", &stubHealthModule{healthErr: errors.New("disk full")})
	srv := NewHealthServer(reg)

	resp, err := srv.Check(context.Background(), &healthv1.HealthCheckRequest{ModuleId: "mod-b"})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if resp.GetStatus() != healthv1.HealthCheckResponse_STATUS_DEGRADED {
		t.Errorf("expected DEGRADED, got %s", resp.GetStatus())
	}
	if resp.GetError() == "" {
		t.Error("expected error message for degraded module")
	}
}

func TestHealthServer_Check_ModuleNotFound(t *testing.T) {
	reg := newStubHealthRegistry()
	srv := NewHealthServer(reg)

	resp, err := srv.Check(context.Background(), &healthv1.HealthCheckRequest{ModuleId: "no-such"})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if resp.GetStatus() != healthv1.HealthCheckResponse_STATUS_UNHEALTHY {
		t.Errorf("expected UNHEALTHY for unknown module, got %s", resp.GetStatus())
	}
}

func TestHealthServer_Check_NodeHealthy(t *testing.T) {
	reg := newStubHealthRegistry()
	reg.add("mod-a", &stubHealthModule{})
	reg.add("mod-b", &stubHealthModule{})
	srv := NewHealthServer(reg)

	resp, err := srv.Check(context.Background(), &healthv1.HealthCheckRequest{})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if resp.GetStatus() != healthv1.HealthCheckResponse_STATUS_HEALTHY {
		t.Errorf("expected HEALTHY for node, got %s", resp.GetStatus())
	}
}

func TestHealthServer_Check_NodeDegraded(t *testing.T) {
	reg := newStubHealthRegistry()
	reg.add("mod-a", &stubHealthModule{})
	reg.add("mod-broken", &stubHealthModule{healthErr: errors.New("oom")})
	srv := NewHealthServer(reg)

	resp, err := srv.Check(context.Background(), &healthv1.HealthCheckRequest{})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if resp.GetStatus() != healthv1.HealthCheckResponse_STATUS_DEGRADED {
		t.Errorf("expected DEGRADED for node with unhealthy module, got %s", resp.GetStatus())
	}
}

func TestHealthServer_Watch_StreamsHealth(t *testing.T) {
	reg := newStubHealthRegistry()
	reg.add("mod-a", &stubHealthModule{})
	srv := NewHealthServer(reg)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	stream := &fakeHealthWatchServer{ctx: ctx}
	done := make(chan error, 1)
	go func() {
		done <- srv.Watch(&healthv1.HealthWatchRequest{
			ModuleIds:       []string{"mod-a"},
			IntervalSeconds: 1,
		}, stream)
	}()

	// Wait for at least one tick.
	time.Sleep(1500 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Watch returned error: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Watch did not stop after context cancel")
	}

	stream.mu.Lock()
	n := len(stream.sent)
	stream.mu.Unlock()
	if n == 0 {
		t.Error("expected at least one health response via Watch")
	}
}

func TestHealthServer_Watch_MultipleModules(t *testing.T) {
	reg := newStubHealthRegistry()
	reg.add("mod-a", &stubHealthModule{})
	reg.add("mod-b", &stubHealthModule{healthErr: errors.New("timeout")})
	reg.add("mod-c", &stubHealthModule{})
	srv := NewHealthServer(reg)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	stream := &fakeHealthWatchServer{ctx: ctx}
	done := make(chan error, 1)
	go func() {
		done <- srv.Watch(&healthv1.HealthWatchRequest{
			ModuleIds:       []string{"mod-a", "mod-b", "mod-c"},
			IntervalSeconds: 1,
		}, stream)
	}()

	time.Sleep(1500 * time.Millisecond)
	cancel()

	<-done

	stream.mu.Lock()
	n := len(stream.sent)
	// Each tick sends 3 module responses + 1 node response = 4 per tick.
	// We expect at least one tick's worth of responses.
	stream.mu.Unlock()
	if n < 4 {
		t.Errorf("expected at least 4 responses (3 modules + 1 node), got %d", n)
	}
}

func TestHealthServer_Watch_NodeResponse(t *testing.T) {
	reg := newStubHealthRegistry()
	reg.add("mod-a", &stubHealthModule{})
	srv := NewHealthServer(reg)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	stream := &fakeHealthWatchServer{ctx: ctx}
	done := make(chan error, 1)
	go func() {
		done <- srv.Watch(&healthv1.HealthWatchRequest{
			ModuleIds:       []string{"mod-a"},
			IntervalSeconds: 1,
		}, stream)
	}()

	time.Sleep(1500 * time.Millisecond)
	cancel()
	<-done

	stream.mu.Lock()
	defer stream.mu.Unlock()

	var hasNodeResp bool
	for _, resp := range stream.sent {
		if resp.GetModuleId() == "" {
			hasNodeResp = true
			break
		}
	}
	if !hasNodeResp {
		t.Error("expected at least one node-level health response in Watch stream")
	}
}

func TestHealthServer_RegisterWithGRPC(t *testing.T) {
	reg := newStubHealthRegistry()
	reg.add("mod-a", &stubHealthModule{})
	srv := NewHealthServer(reg)

	var lc net.ListenConfig
	lis, err := lc.Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	grpcSrv := grpc.NewServer()
	srv.RegisterWithGRPC(grpcSrv)
	go grpcSrv.Serve(lis)
	t.Cleanup(grpcSrv.GracefulStop)

	conn, err := grpc.NewClient(lis.Addr().String(),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { conn.Close() })

	client := healthv1.NewHealthServiceClient(conn)
	resp, err := client.Check(context.Background(), &healthv1.HealthCheckRequest{ModuleId: "mod-a"})
	if err != nil {
		t.Fatalf("Check via gRPC: %v", err)
	}
	if resp.GetStatus() != healthv1.HealthCheckResponse_STATUS_HEALTHY {
		t.Errorf("expected HEALTHY, got %s", resp.GetStatus())
	}
}

func TestHealthServer_Watch_ClampsInterval(t *testing.T) {
	reg := newStubHealthRegistry()
	reg.add("mod-a", &stubHealthModule{})
	srv := NewHealthServer(reg)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	stream := &fakeHealthWatchServer{ctx: ctx}
	done := make(chan error, 1)
	go func() {
		done <- srv.Watch(&healthv1.HealthWatchRequest{
			ModuleIds:       []string{"mod-a"},
			IntervalSeconds: 0, // should clamp to default 30
		}, stream)
	}()

	// Should not produce output immediately with 30s interval.
	time.Sleep(100 * time.Millisecond)
	cancel()
	<-done

	stream.mu.Lock()
	n := len(stream.sent)
	stream.mu.Unlock()
	if n != 0 {
		t.Errorf("expected 0 responses with 30s clamped interval in short window, got %d", n)
	}
}

func TestHealthServer_Watch_ClampsMaxInterval(t *testing.T) {
	reg := newStubHealthRegistry()
	reg.add("mod-a", &stubHealthModule{})
	srv := NewHealthServer(reg)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	stream := &fakeHealthWatchServer{ctx: ctx}
	done := make(chan error, 1)
	go func() {
		done <- srv.Watch(&healthv1.HealthWatchRequest{
			ModuleIds:       []string{"mod-a"},
			IntervalSeconds: 9999, // should clamp to 300
		}, stream)
	}()

	time.Sleep(100 * time.Millisecond)
	cancel()
	<-done

	stream.mu.Lock()
	n := len(stream.sent)
	stream.mu.Unlock()
	if n != 0 {
		t.Errorf("expected 0 responses with clamped max interval in short window, got %d", n)
	}
}
