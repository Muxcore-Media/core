package module

import (
	"context"
	"errors"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/Muxcore-Media/core/pkg/contracts"
	healthv1 "github.com/Muxcore-Media/core/proto/gen/muxcore/health/v1"
	modulev1 "github.com/Muxcore-Media/core/proto/gen/muxcore/module/v1"
)

// fakeCore is a minimal core: ModuleRegistration plus (optionally) the
// muxcore HealthService, backed by an in-memory set of registered IDs.
type fakeCore struct {
	modulev1.UnimplementedModuleRegistrationServer
	healthv1.UnimplementedHealthServiceServer

	mu         sync.Mutex
	modules    map[string]bool
	registers  int
	unregs     int
	events     []string // "register:<id>", "unregister:<id>"
	callerIDs  []string // x-caller-id seen by Health/Check
	registered chan string
}

func newFakeCore() *fakeCore {
	return &fakeCore{modules: map[string]bool{}, registered: make(chan string, 16)}
}

func (f *fakeCore) Register(_ context.Context, req *modulev1.RegisterRequest) (*modulev1.RegisterResponse, error) {
	f.mu.Lock()
	id := req.GetModuleInfo().GetId()
	f.modules[id] = true
	f.registers++
	f.events = append(f.events, "register:"+id)
	f.mu.Unlock()
	f.registered <- id
	return &modulev1.RegisterResponse{Accepted: true}, nil
}

func (f *fakeCore) Unregister(_ context.Context, req *modulev1.UnregisterRequest) (*modulev1.UnregisterResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.unregs++
	f.events = append(f.events, "unregister:"+req.GetModuleId())
	ok := f.modules[req.GetModuleId()]
	delete(f.modules, req.GetModuleId())
	return &modulev1.UnregisterResponse{Acknowledged: ok}, nil
}

func (f *fakeCore) Check(ctx context.Context, req *healthv1.HealthCheckRequest) (*healthv1.HealthCheckResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if md, ok := metadata.FromIncomingContext(ctx); ok {
		f.callerIDs = append(f.callerIDs, md.Get("x-caller-id")...)
	}
	if !f.modules[req.GetModuleId()] {
		return &healthv1.HealthCheckResponse{ModuleId: req.GetModuleId(),
			Status: healthv1.HealthCheckResponse_STATUS_UNHEALTHY, Error: "not found"}, nil
	}
	return &healthv1.HealthCheckResponse{ModuleId: req.GetModuleId(), Status: healthv1.HealthCheckResponse_STATUS_HEALTHY}, nil
}

func (f *fakeCore) forget(id string) {
	f.mu.Lock()
	delete(f.modules, id)
	f.mu.Unlock()
}

func (f *fakeCore) snapshot() (registers, unregs int, events []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.registers, f.unregs, append([]string(nil), f.events...)
}

// serve starts f on lis; withHealth also registers the HealthService.
func (f *fakeCore) serve(t *testing.T, lis net.Listener, withHealth bool) *grpc.Server {
	t.Helper()
	srv := grpc.NewServer()
	modulev1.RegisterModuleRegistrationServer(srv, f)
	if withHealth {
		healthv1.RegisterHealthServiceServer(srv, f)
	}
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)
	return srv
}

func listen(t *testing.T, addr string) net.Listener {
	t.Helper()
	lis, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	return lis
}

// orderModule records Stop so tests can check it runs after Unregister.
type orderModule struct {
	testModule
	core    *fakeCore
	stopped atomic.Bool
}

func (m *orderModule) Stop(_ context.Context) error {
	m.core.mu.Lock()
	m.core.events = append(m.core.events, "stop")
	m.core.mu.Unlock()
	m.stopped.Store(true)
	return nil
}

// startedModule signals when Start returns, i.e. Run has finished the
// initial registration.
type startedModule struct {
	contracts.Module
	started chan struct{}
}

func (m *startedModule) Start(ctx context.Context) error {
	err := m.Module.Start(ctx)
	close(m.started)
	return err
}

// startRun runs mod under RunContext and waits until it has started. wait
// returns RunContext's result (cancel first).
func startRun(t *testing.T, addr string, mod contracts.Module, interval time.Duration) (cancel func(), wait func(timeout time.Duration) error) {
	t.Helper()
	t.Setenv("MUXCORE_PROFILE", "dev")
	sm := &startedModule{Module: mod, started: make(chan struct{})}
	ctx, cancelFn := context.WithCancel(context.Background())
	ch := make(chan error, 1)
	go func() {
		ch <- RunContext(ctx, Config{Module: sm, GRPCAddr: addr, Insecure: true, SupervisionInterval: interval})
	}()
	var (
		once   sync.Once
		result error
	)
	wait = func(timeout time.Duration) error {
		once.Do(func() {
			select {
			case result = <-ch:
			case <-time.After(timeout):
				result = errors.New("RunContext did not return")
			}
		})
		return result
	}
	t.Cleanup(func() {
		cancelFn()
		if err := wait(10 * time.Second); err != nil {
			t.Error(err)
		}
	})
	select {
	case <-sm.started:
	case err := <-ch:
		t.Fatalf("RunContext returned before start: %v", err)
	case <-time.After(10 * time.Second):
		t.Fatal("module did not start")
	}
	return cancelFn, wait
}

func waitRegistered(t *testing.T, f *fakeCore, want string) {
	t.Helper()
	select {
	case id := <-f.registered:
		if id != want {
			t.Fatalf("registered %q, want %q", id, want)
		}
	case <-time.After(10 * time.Second):
		t.Fatalf("timed out waiting for %q to register", want)
	}
}

// Cancelling the run context unregisters from core, before Stop, and
// RunContext returns nil.
func TestRunContext_UnregistersOnCancel(t *testing.T) {
	f := newFakeCore()
	lis := listen(t, "127.0.0.1:0")
	f.serve(t, lis, true)

	mod := &orderModule{testModule: testModule{info: contracts.ModuleInfo{ID: "media-rename", Name: "Rename", Version: "1.0.0"}}, core: f}
	cancel, wait := startRun(t, lis.Addr().String(), mod, -1)
	waitRegistered(t, f, "media-rename")

	cancel()
	if err := wait(10 * time.Second); err != nil {
		t.Fatalf("RunContext: %v", err)
	}
	_, unregs, events := f.snapshot()
	if unregs != 1 {
		t.Fatalf("unregister calls = %d, want 1", unregs)
	}
	want := []string{"register:media-rename", "unregister:media-rename", "stop"}
	if len(events) != len(want) {
		t.Fatalf("events = %v, want %v", events, want)
	}
	for i := range want {
		if events[i] != want[i] {
			t.Fatalf("events = %v, want %v", events, want)
		}
	}
}

// Shutdown is not held up by an unreachable core: Unregister is best effort
// with a short timeout.
func TestRunContext_UnregisterBestEffort(t *testing.T) {
	f := newFakeCore()
	lis := listen(t, "127.0.0.1:0")
	srv := f.serve(t, lis, true)

	mod := &orderModule{testModule: testModule{info: contracts.ModuleInfo{ID: "m1", Name: "m1"}}, core: f}
	cancel, wait := startRun(t, lis.Addr().String(), mod, -1)
	waitRegistered(t, f, "m1")
	srv.Stop()

	start := time.Now()
	cancel()
	if err := wait(unregisterTimeout + 5*time.Second); err != nil {
		t.Fatalf("RunContext: %v", err)
	}
	if el := time.Since(start); el > unregisterTimeout+2*time.Second {
		t.Fatalf("shutdown took %v", el)
	}
	if !mod.stopped.Load() {
		t.Fatal("module not stopped")
	}
}

// When core forgets the module (restart, recreate), the supervisor
// re-registers it.
func TestRunContext_ReregistersWhenCoreForgets(t *testing.T) {
	f := newFakeCore()
	lis := listen(t, "127.0.0.1:0")
	f.serve(t, lis, true)

	mod := &testModule{info: contracts.ModuleInfo{ID: "media-rename", Name: "Rename"}}
	startRun(t, lis.Addr().String(), mod, 20*time.Millisecond)
	waitRegistered(t, f, "media-rename")

	// A few healthy probes: no re-registration while core knows the module.
	time.Sleep(150 * time.Millisecond)
	if n, _, _ := f.snapshot(); n != 1 {
		t.Fatalf("registers while known = %d, want 1", n)
	}

	f.forget("media-rename")
	waitRegistered(t, f, "media-rename")

	f.mu.Lock()
	ids := append([]string(nil), f.callerIDs...)
	f.mu.Unlock()
	if len(ids) == 0 || ids[0] != "media-rename" {
		t.Fatalf("health probe x-caller-id = %v", ids)
	}
}

// Core restarts on the same address without the HealthService (an older
// core that cannot answer): the module re-registers once core is back.
func TestRunContext_ReregistersAfterCoreRestart(t *testing.T) {
	f1 := newFakeCore()
	lis := listen(t, "127.0.0.1:0")
	addr := lis.Addr().String()
	srv1 := f1.serve(t, lis, false)

	mod := &testModule{info: contracts.ModuleInfo{ID: "m1", Name: "m1"}}
	startRun(t, addr, mod, 20*time.Millisecond)
	waitRegistered(t, f1, "m1")

	srv1.Stop()
	time.Sleep(200 * time.Millisecond) // supervisor observes the outage

	f2 := newFakeCore()
	var lis2 net.Listener
	deadline := time.Now().Add(5 * time.Second)
	for {
		l, err := net.Listen("tcp", addr)
		if err == nil {
			lis2 = l
			break
		}
		if time.Now().After(deadline) {
			t.Skipf("cannot rebind %s: %v", addr, err)
		}
		time.Sleep(50 * time.Millisecond)
	}
	f2.serve(t, lis2, false)
	waitRegistered(t, f2, "m1")

	// Re-registered once; no repeat while core stays up.
	time.Sleep(150 * time.Millisecond)
	if n, _, _ := f2.snapshot(); n != 1 {
		t.Fatalf("registers on restarted core = %d, want 1", n)
	}
}

func TestSupervisor_Decisions(t *testing.T) {
	cases := []struct {
		name   string
		probes []probeResult
		want   int // register calls
	}{
		{"known", []probeResult{probeKnown, probeKnown}, 0},
		{"unknown", []probeResult{probeUnknown, probeKnown}, 1},
		{"unsupported only", []probeResult{probeUnsupported, probeUnsupported}, 0},
		{"unreachable then unsupported", []probeResult{probeUnreachable, probeUnreachable, probeUnsupported, probeUnsupported}, 1},
		{"unreachable then known", []probeResult{probeUnreachable, probeKnown, probeUnsupported}, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := runSupervisor(t, tc.probes, nil)
			if got != tc.want {
				t.Fatalf("register calls = %d, want %d", got, tc.want)
			}
		})
	}
}

// A failing re-registration is retried (with backoff) until it succeeds,
// without re-probing success into extra registrations.
func TestSupervisor_RetriesFailedRegistration(t *testing.T) {
	errs := []error{errors.New("boom"), errors.New("boom"), nil}
	got := runSupervisor(t, []probeResult{probeUnknown, probeUnknown, probeUnknown, probeKnown, probeKnown}, errs)
	if got != 3 {
		t.Fatalf("register calls = %d, want 3", got)
	}
}

// runSupervisor feeds probes to a supervisor (then probeKnown forever) and
// returns the number of register calls once all probes were consumed.
func runSupervisor(t *testing.T, probes []probeResult, regErrs []error) int {
	t.Helper()
	var mu sync.Mutex
	i, calls := 0, 0
	consumed := make(chan struct{})
	s := &supervisor{
		moduleID: "m",
		interval: time.Millisecond,
		probe: func(context.Context) probeResult {
			mu.Lock()
			defer mu.Unlock()
			if i == len(probes) {
				close(consumed)
			}
			i++
			if i <= len(probes) {
				return probes[i-1]
			}
			return probeKnown
		},
		register: func(context.Context) error {
			mu.Lock()
			defer mu.Unlock()
			calls++
			if calls <= len(regErrs) {
				return regErrs[calls-1]
			}
			return nil
		},
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { s.run(ctx); close(done) }()
	select {
	case <-consumed:
	case <-time.After(10 * time.Second):
		t.Fatal("supervisor did not consume probes")
	}
	cancel()
	<-done
	mu.Lock()
	defer mu.Unlock()
	return calls
}

func TestSupervisor_Backoff(t *testing.T) {
	s := &supervisor{interval: 15 * time.Second}
	for n, want := range map[int]time.Duration{1: time.Second, 2: 2 * time.Second, 4: 8 * time.Second, 7: time.Minute, 50: time.Minute} {
		if got := s.backoff(n); got != want {
			t.Errorf("backoff(%d) = %v, want %v", n, got, want)
		}
	}
	short := &supervisor{interval: 10 * time.Millisecond}
	if got := short.backoff(1); got != 10*time.Millisecond {
		t.Errorf("short backoff(1) = %v", got)
	}
}

// "already registered" from core means core knows the module.
func TestRegisterFunc_AlreadyRegisteredIsSuccess(t *testing.T) {
	c := &stubRegClient{resp: &modulev1.RegisterResponse{Accepted: false, Error: `module "m" already registered`}}
	if err := registerFunc(c, &modulev1.RegisterRequest{}, time.Second)(context.Background()); err != nil {
		t.Fatal(err)
	}
	c.resp = &modulev1.RegisterResponse{Accepted: false, Error: "nope"}
	if err := registerFunc(c, &modulev1.RegisterRequest{}, time.Second)(context.Background()); err == nil {
		t.Fatal("expected rejection error")
	}
}

type stubRegClient struct {
	modulev1.ModuleRegistrationClient
	resp *modulev1.RegisterResponse
}

func (c *stubRegClient) Register(context.Context, *modulev1.RegisterRequest, ...grpc.CallOption) (*modulev1.RegisterResponse, error) {
	return c.resp, nil
}

type refusingHealth struct {
	healthv1.UnimplementedHealthServiceServer
	calls atomic.Int32
}

func (h *refusingHealth) Check(context.Context, *healthv1.HealthCheckRequest) (*healthv1.HealthCheckResponse, error) {
	h.calls.Add(1)
	return nil, status.Error(codes.PermissionDenied, "denied")
}

// A core that refuses the health probe is asked once; later probes use the
// connection state so they do not accumulate authentication failures.
func TestHealthProbe_RefusedFallsBackToConnState(t *testing.T) {
	lis := listen(t, "127.0.0.1:0")
	srv := grpc.NewServer()
	h := &refusingHealth{}
	healthv1.RegisterHealthServiceServer(srv, h)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)

	conn, err := dialGRPC(lis.Addr().String(), dialTLSConfig{plaintext: true})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	probe := healthProbe(conn, "m1", time.Second)
	for i := range 3 {
		if got := probe(context.Background()); got != probeUnsupported {
			t.Fatalf("probe %d = %v, want unsupported", i, got)
		}
	}
	if n := h.calls.Load(); n != 1 {
		t.Fatalf("health Check calls = %d, want 1", n)
	}
}
