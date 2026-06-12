//go:build integration

// Package integration contains end-to-end tests that spin up real core
// subsystems in-process. Run with:
//
//	go test -tags=integration -timeout 60s ./internal/integration/
//
// These tests are excluded from `make test` deliberately — they're slower
// and may conflict with other tests binding to network ports.
package integration

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"errors"
	"github.com/Muxcore-Media/core/internal/audit"
	"github.com/Muxcore-Media/core/internal/config"
	"github.com/Muxcore-Media/core/internal/events"
	"github.com/Muxcore-Media/core/internal/grpcmesh"
	"github.com/Muxcore-Media/core/internal/health"
	"github.com/Muxcore-Media/core/internal/module"
	"github.com/Muxcore-Media/core/internal/registry"
	"github.com/Muxcore-Media/core/internal/storage"
	"github.com/Muxcore-Media/core/pkg/contracts"
	storagev1 "github.com/Muxcore-Media/core/proto/gen/muxcore/storage/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

type allowAllPublishPolicy struct{}

func (*allowAllPublishPolicy) CanPublish(ctx context.Context, callerID, eventType string) (bool, error) {
	return true, nil
}

type allowAllCallPolicy struct{}

func (*allowAllCallPolicy) AllowCall(ctx context.Context, callerModuleID, targetModuleID, method string) (bool, error) {
	return true, nil
}

// harness wires up the core subsystems in-process for testing.
// No external processes, no spool, no TLS.
type harness struct {
	cfg        *config.Config
	bus        *events.MemoryBus
	reg        *registry.Registry
	store      *storage.Orchestrator
	modMgr     *module.Manager
	meshSrv    *grpcmesh.Server
	meshClient *grpcmesh.Client
	grpcSrv    *grpc.Server
	grpcAddr   string
	audit      *audit.FileLogger
	cancel     context.CancelFunc
	ctx        context.Context
	t          *testing.T
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	os.Setenv("MUXCORE_DEV_TLS_SKIP", "true")

	cfg := config.Default()
	ctx, cancel := context.WithCancel(context.Background())

	bus := events.NewMemoryBus()
	reg := registry.New()
	store := storage.NewOrchestrator(reg)

	// Audit logger — no-op for tests.
	auditLogger, _ := audit.NewFileLogger("")
	bus.SetAuditLogger(auditLogger)

	// Allow-all publish policy for tests.
	bus.SetPublishPolicy(&allowAllPublishPolicy{})
	modMgr := module.NewManager(reg, bus)

	// gRPC server on a random port.
	var lc net.ListenConfig
	lis, err := lc.Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	grpcSrv := grpc.NewServer(grpc.Creds(insecure.NewCredentials()))

	meshSrv := grpcmesh.NewServer()
	meshSrv.RegisterWithGRPC(grpcSrv)
	meshClient := grpcmesh.NewClient(meshSrv)
	// Allow-all call policy for tests.
	storageGrpc := grpcmesh.NewStorageServer(store)
	storageGrpc.SetCallPolicy(&allowAllCallPolicy{})
	storageGrpc.RegisterWithGRPC(grpcSrv)
	healthSrv := grpcmesh.NewHealthServer(reg)
	healthSrv.RegisterWithGRPC(grpcSrv)

	go grpcSrv.Serve(lis)

	h := &harness{
		cfg:        cfg,
		bus:        bus,
		reg:        reg,
		store:      store,
		modMgr:     modMgr,
		meshSrv:    meshSrv,
		meshClient: meshClient,
		grpcSrv:    grpcSrv,
		grpcAddr:   lis.Addr().String(),
		audit:      auditLogger,
		cancel:     cancel,
		ctx:        ctx,
		t:          t,
	}
	t.Cleanup(h.stop)
	return h
}

func (h *harness) stop() {
	h.cancel()
	h.grpcSrv.GracefulStop()
}

// --- Tests ---

func TestIntegration_ModuleRegisterAndUnregister(t *testing.T) {
	h := newHarness(t)

	mod := &stubModule{id: "test-mod", name: "Test Module", version: "1.0.0"}
	if err := h.modMgr.Register(mod, nil); err != nil {
		t.Fatalf("Register: %v", err)
	}

	// Module should appear in the registry.
	entry, err := h.reg.Get("test-mod")
	if err != nil {
		t.Fatalf("Get after Register: %v", err)
	}
	if entry.Info.Name != "Test Module" {
		t.Errorf("expected name 'Test Module', got %q", entry.Info.Name)
	}

	// Unregister should remove it.
	if err := h.modMgr.Unregister("test-mod"); err != nil {
		t.Fatalf("Unregister: %v", err)
	}
	if _, err := h.reg.Get("test-mod"); err == nil {
		t.Error("expected module to be removed from registry")
	}
}

func TestIntegration_ModuleLifecycle_InitStartStop(t *testing.T) {
	h := newHarness(t)

	mod := &stubModule{id: "lifecycle-mod", name: "Lifecycle", version: "1.0.0"}
	if err := h.modMgr.Register(mod, nil); err != nil {
		t.Fatalf("Register: %v", err)
	}

	if err := h.modMgr.InitAll(h.ctx); err != nil {
		t.Fatalf("InitAll: %v", err)
	}
	if !mod.inited {
		t.Error("expected Init() to be called")
	}

	if err := h.modMgr.StartAll(h.ctx); err != nil {
		t.Fatalf("StartAll: %v", err)
	}
	if !mod.started {
		t.Error("expected Start() to be called")
	}

	if err := h.modMgr.StopAll(h.ctx); err != nil {
		t.Fatalf("StopAll: %v", err)
	}
	if !mod.stopped {
		t.Error("expected Stop() to be called")
	}
}

func TestIntegration_EventBus_PublishAndSubscribe(t *testing.T) {
	h := newHarness(t)

	received := make(chan contracts.Event, 4)
	ctx := context.Background()

	_, err := h.bus.Subscribe(ctx, "test.event", func(ctx context.Context, e contracts.Event) error {
		received <- e
		return nil
	})
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	if err := h.bus.Publish(ctx, contracts.Event{
		Type:    "test.event",
		Source:  "",
		Payload: []byte(`"hello"`),
	}); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	select {
	case ev := <-received:
		if string(ev.Payload) != `"hello"` {
			t.Errorf("unexpected payload: %s", ev.Payload)
		}
	case <-time.After(2 * time.Second):
		t.Error("timeout waiting for event delivery")
	}
}

func TestIntegration_EventBus_ModuleLifecycleEvents(t *testing.T) {
	h := newHarness(t)

	registered := make(chan string, 1)
	ctx := context.Background()

	h.bus.Subscribe(ctx, contracts.EventModuleRegistered, func(ctx context.Context, e contracts.Event) error {
		registered <- string(e.Payload)
		return nil
	})

	mod := &stubModule{id: "event-mod", name: "Event Mod", version: "1.0.0"}
	if err := h.modMgr.Register(mod, nil); err != nil {
		t.Fatalf("Register: %v", err)
	}

	select {
	case payload := <-registered:
		if payload == "" {
			t.Error("expected non-empty module.registered payload")
		}
	case <-time.After(2 * time.Second):
		t.Error("timeout: module.registered event not received")
	}
}

func TestIntegration_StorageOrchestrator_PutGet(t *testing.T) {
	h := newHarness(t)

	// Register a mock storage provider.
	prov := newMemStorage()
	mod := &storageModule{id: "storage-local", prov: prov}
	if err := h.modMgr.Register(mod, nil); err != nil {
		t.Fatalf("Register storage module: %v", err)
	}
	if err := h.store.DiscoverStorage(); err != nil {
		t.Fatalf("DiscoverStorage: %v", err)
	}

	ctx := context.Background()
	key := "media/test-file.txt"
	content := []byte("integration test content")

	if err := h.store.Put(ctx, key, bytes.NewReader(content), int64(len(content))); err != nil {
		t.Fatalf("Put: %v", err)
	}

	rc, err := h.store.Get(ctx, key)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer rc.Close()

	got, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if string(got) != string(content) {
		t.Errorf("Get returned %q, want %q", got, content)
	}
}

func TestIntegration_StorageOrchestrator_Delete(t *testing.T) {
	h := newHarness(t)

	prov := newMemStorage()
	mod := &storageModule{id: "storage-del", prov: prov}
	h.modMgr.Register(mod, nil)
	h.store.DiscoverStorage()

	ctx := context.Background()
	key := "media/to-delete.txt"

	h.store.Put(ctx, key, bytes.NewReader([]byte("bye")), 3)

	if err := h.store.Delete(ctx, key); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	exists, err := h.store.Exists(ctx, key)
	if err != nil {
		t.Fatalf("Exists after delete: %v", err)
	}
	if exists {
		t.Error("expected key to not exist after Delete")
	}
}

func TestIntegration_Health_CoreProbes(t *testing.T) {
	h := newHarness(t)

	coreH := health.New()
	coreH.RegisterProbe("event_bus", func(ctx context.Context) error {
		if h.bus.SubscriberCount() < 0 {
			return fmt.Errorf("bus not initialized")
		}
		return nil
	})
	coreH.RegisterProbe("registry", func(ctx context.Context) error {
		return nil
	})

	results := coreH.Check(context.Background())
	for name, err := range results {
		if err != nil {
			t.Errorf("probe %q failed: %v", name, err)
		}
	}
	if !coreH.Healthy(context.Background()) {
		t.Error("expected all probes to pass")
	}
}

func TestIntegration_gRPC_StorageServer(t *testing.T) {
	h := newHarness(t)

	prov := newMemStorage()
	mod := &storageModule{id: "storage-grpc", prov: prov}
	h.modMgr.Register(mod, nil)
	h.store.DiscoverStorage()

	// Connect to the gRPC storage server over the wire.
	conn, err := grpc.NewClient(h.grpcAddr,
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial gRPC: %v", err)
	}
	defer conn.Close()

	client := storagev1.NewStorageServiceClient(conn)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Put via gRPC.
	stream, err := client.Put(ctx)
	if err != nil {
		t.Fatalf("Put stream: %v", err)
	}
	stream.Send(&storagev1.PutRequest{Key: "grpc/test.txt", Chunk: []byte("grpc content"), TotalSize: 12})
	if _, err := stream.CloseAndRecv(); err != nil {
		t.Fatalf("Put CloseAndRecv: %v", err)
	}

	// Get via gRPC.
	getStream, err := client.Get(ctx, &storagev1.GetRequest{Key: "grpc/test.txt"})
	if err != nil {
		t.Fatalf("Get stream: %v", err)
	}
	var data []byte
	for {
		chunk, err := getStream.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("Get Recv: %v", err)
		}
		data = append(data, chunk.GetChunk()...)
	}
	if string(data) != "grpc content" {
		t.Errorf("gRPC Get returned %q, want 'grpc content'", data)
	}
}

func TestIntegration_Registry_DependencyOrder(t *testing.T) {
	h := newHarness(t)

	a := &stubModule{id: "mod-a", name: "A", version: "1.0.0"}
	b := &stubModule{id: "mod-b", name: "B", version: "1.0.0"}
	// B depends on A.
	h.modMgr.Register(a, nil)
	h.modMgr.Register(b, []string{"mod-a"})

	order, err := h.reg.StartupOrder()
	if err != nil {
		t.Fatalf("StartupOrder: %v", err)
	}
	if len(order) < 2 {
		t.Fatalf("expected 2 modules in order, got %d", len(order))
	}

	aIdx, bIdx := -1, -1
	for i, id := range order {
		if id == "mod-a" {
			aIdx = i
		}
		if id == "mod-b" {
			bIdx = i
		}
	}
	if aIdx == -1 || bIdx == -1 {
		t.Fatalf("mod-a or mod-b not in startup order: %v", order)
	}
	if aIdx >= bIdx {
		t.Errorf("expected mod-a before mod-b in startup order, got %v", order)
	}
}

// TestIntegration_ConfigReload_SafeChanges verifies that the config reload
// mechanism detects safe changes (log level, audit path, seed nodes) and
// marks them correctly via the ChangedFields struct.
func TestIntegration_ConfigReload_SafeChanges(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "muxcore.json")

	// Write initial config.
	initial := `{"server":{"addr":":8080"},"log":{"level":"info","format":"text"}}`
	if err := os.WriteFile(path, []byte(initial), 0600); err != nil {
		t.Fatal(err)
	}

	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("initial Load: %v", err)
	}

	// Write updated config with safe changes.
	updated := `{"server":{"addr":":8080"},"log":{"level":"debug","format":"json"}}`
	if err := os.WriteFile(path, []byte(updated), 0600); err != nil {
		t.Fatal(err)
	}

	result, err := config.Reload(cfg, path)
	if err != nil {
		t.Fatalf("Reload: %v", err)
	}

	if !result.Changes.LogLevel {
		t.Error("expected LogLevel change detected")
	}
	if !result.Changes.LogFormat {
		t.Error("expected LogFormat change detected")
	}
	if result.Changes.Unsafe {
		t.Errorf("expected no unsafe changes, got: %v", result.Changes.UnsafeFields)
	}
}

// TestIntegration_ConfigReload_UnsafeChanges verifies that changes requiring
// a restart (server address, TLS certs, etc.) are correctly flagged as unsafe.
func TestIntegration_ConfigReload_UnsafeChanges(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "muxcore.json")

	initial := `{"server":{"addr":":8080"},"grpc":{"addr":":9090"}}`
	if err := os.WriteFile(path, []byte(initial), 0600); err != nil {
		t.Fatal(err)
	}

	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("initial Load: %v", err)
	}

	updated := `{"server":{"addr":":9999"},"grpc":{"addr":":9090"}}`
	if err := os.WriteFile(path, []byte(updated), 0600); err != nil {
		t.Fatal(err)
	}

	result, err := config.Reload(cfg, path)
	if err != nil {
		t.Fatalf("Reload: %v", err)
	}

	if !result.Changes.Unsafe {
		t.Fatal("expected unsafe changes (server.addr changed)")
	}
	found := false
	for _, f := range result.Changes.UnsafeFields {
		if f == "server.addr" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected 'server.addr' in unsafe fields, got: %v", result.Changes.UnsafeFields)
	}
}

// TestIntegration_ConfigReload_DeletedFile verifies that Reload returns a
// clear error when the config file is deleted between Load and Reload, and
// does not panic or corrupt the running config.
func TestIntegration_ConfigReload_DeletedFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "muxcore.json")

	if err := os.WriteFile(path, []byte(`{"server":{"addr":":8080"}}`), 0600); err != nil {
		t.Fatal(err)
	}

	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("initial Load: %v", err)
	}

	os.Remove(path)

	_, err = config.Reload(cfg, path)
	if err == nil {
		t.Error("expected error when reloading after file deletion")
	}
}

// TestIntegration_MeshClient_DenyByDefault verifies that when no call policy
// module is registered, Client.Call() denies all inter-module calls. This is
// the core deny-by-default security invariant for the mesh.
func TestIntegration_MeshClient_DenyByDefault(t *testing.T) {
	h := newHarness(t)

	h.meshSrv.RegisterHandler("target-module", echoMeshHandler{})

	// No call policy set in the harness — all calls must be denied.
	_, err := h.meshClient.Call(context.Background(), "target-module", "Echo", []byte("hello"))
	if err == nil {
		t.Fatal("expected call to be denied when no call policy is configured, got nil error")
	}
	if !strings.Contains(err.Error(), "no call policy configured") {
		t.Errorf("expected 'no call policy configured' in error, got: %v", err)
	}
}

// TestIntegration_MeshClient_WithPolicy_Succeeds verifies that when a call
// policy that allows the call is configured, the mesh Call returns the
// handler's response. This exercises the full allow path: policy check →
// route to handler → return response.
func TestIntegration_MeshClient_WithPolicy_Succeeds(t *testing.T) {
	h := newHarness(t)

	h.meshSrv.RegisterHandler("echo-mod", echoMeshHandler{})
	h.meshClient.SetCallPolicy(&allowAllCallPolicy{})

	payload := []byte("ping")
	resp, err := h.meshClient.Call(context.Background(), "echo-mod", "Echo", payload)
	if err != nil {
		t.Fatalf("Call with allow policy: %v", err)
	}
	if string(resp) != string(payload) {
		t.Errorf("expected response %q, got %q", payload, resp)
	}
}

// TestIntegration_EventBus_RequestReply verifies the request/reply pattern:
// a subscriber listens on the reply event type, and a publisher sends a request
// and receives the reply within the timeout.
func TestIntegration_EventBus_RequestReply(t *testing.T) {
	h := newHarness(t)

	ctx := context.Background()
	replyType := contracts.ReplyEventType("test.request")
	replyPayload := []byte(`"response-data"`)

	// Register a handler that replies to requests on "test.request".
	_, err := h.bus.Subscribe(ctx, "test.request", func(ctx context.Context, e contracts.Event) error {
		return h.bus.Publish(ctx, contracts.Event{
			Type:    replyType,
			Source:  "",
			Payload: replyPayload,
		})
	})
	if err != nil {
		t.Fatalf("Subscribe request handler: %v", err)
	}

	resp, err := h.bus.Request(ctx, contracts.Event{
		Type:    "test.request",
		Source:  "test",
		Payload: []byte(`"hello"`),
	}, 2*time.Second)
	if err != nil {
		t.Fatalf("Request: %v", err)
	}
	if string(resp.Payload) != string(replyPayload) {
		t.Errorf("expected reply %q, got %q", replyPayload, resp.Payload)
	}
}

// --- Stubs ---

type stubModule struct {
	id, name, version        string
	roles, caps              []string
	inited, started, stopped bool
}

func (m *stubModule) Info() contracts.ModuleInfo {
	return contracts.ModuleInfo{
		ID:           m.id,
		Name:         m.name,
		Version:      m.version,
		Roles:        m.roles,
		Capabilities: m.caps,
	}
}
func (m *stubModule) Init(_ context.Context) error   { m.inited = true; return nil }
func (m *stubModule) Start(_ context.Context) error  { m.started = true; return nil }
func (m *stubModule) Stop(_ context.Context) error   { m.stopped = true; return nil }
func (m *stubModule) Health(_ context.Context) error { return nil }

// memStorage is a thread-safe in-memory StorageProvider for integration tests.
type memStorage struct {
	mu      sync.RWMutex
	objects map[string][]byte
}

func newMemStorage() *memStorage {
	return &memStorage{objects: make(map[string][]byte)}
}

func (s *memStorage) Put(_ context.Context, key string, data io.Reader, _ int64) error {
	b, _ := io.ReadAll(data)
	s.mu.Lock()
	s.objects[key] = b
	s.mu.Unlock()
	return nil
}
func (s *memStorage) Get(_ context.Context, key string) (io.ReadCloser, error) {
	s.mu.RLock()
	b, ok := s.objects[key]
	s.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("not found: %s", key)
	}
	return io.NopCloser(bytes.NewReader(b)), nil
}
func (s *memStorage) Delete(_ context.Context, key string) error {
	s.mu.Lock()
	delete(s.objects, key)
	s.mu.Unlock()
	return nil
}
func (s *memStorage) Exists(_ context.Context, key string) (bool, error) {
	s.mu.RLock()
	_, ok := s.objects[key]
	s.mu.RUnlock()
	return ok, nil
}
func (s *memStorage) Stat(_ context.Context, key string) (contracts.ObjectInfo, error) {
	s.mu.RLock()
	b, ok := s.objects[key]
	s.mu.RUnlock()
	if !ok {
		return contracts.ObjectInfo{}, fmt.Errorf("not found: %s", key)
	}
	return contracts.ObjectInfo{Key: key, Size: int64(len(b))}, nil
}
func (s *memStorage) Move(_ context.Context, src, dst string) error {
	s.mu.Lock()
	b := s.objects[src]
	delete(s.objects, src)
	s.objects[dst] = b
	s.mu.Unlock()
	return nil
}
func (s *memStorage) List(_ context.Context, _ string) ([]contracts.ObjectInfo, error) {
	s.mu.RLock()
	out := make([]contracts.ObjectInfo, 0, len(s.objects))
	for k, b := range s.objects {
		out = append(out, contracts.ObjectInfo{Key: k, Size: int64(len(b))})
	}
	s.mu.RUnlock()
	return out, nil
}

// storageModule wraps memStorage as a contracts.Module for registration.
type storageModule struct {
	id   string
	prov *memStorage
}

func (m *storageModule) Info() contracts.ModuleInfo {
	return contracts.ModuleInfo{
		ID: m.id, Name: m.id, Version: "1.0.0",
		Roles:        []string{"storage"},
		Capabilities: []string{"storage.local"},
	}
}
func (m *storageModule) Init(_ context.Context) error   { return nil }
func (m *storageModule) Start(_ context.Context) error  { return nil }
func (m *storageModule) Stop(_ context.Context) error   { return nil }
func (m *storageModule) Health(_ context.Context) error { return nil }

// storageModule must also implement contracts.StorageProvider to be picked up by DiscoverStorage.
func (m *storageModule) Put(ctx context.Context, key string, data io.Reader, size int64) error {
	return m.prov.Put(ctx, key, data, size)
}
func (m *storageModule) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	return m.prov.Get(ctx, key)
}
func (m *storageModule) Delete(ctx context.Context, key string) error {
	return m.prov.Delete(ctx, key)
}
func (m *storageModule) Exists(ctx context.Context, key string) (bool, error) {
	return m.prov.Exists(ctx, key)
}
func (m *storageModule) Stat(ctx context.Context, key string) (contracts.ObjectInfo, error) {
	return m.prov.Stat(ctx, key)
}
func (m *storageModule) Move(ctx context.Context, src, dst string) error {
	return m.prov.Move(ctx, src, dst)
}
func (m *storageModule) List(ctx context.Context, prefix string) ([]contracts.ObjectInfo, error) {
	return m.prov.List(ctx, prefix)
}

// echoMeshHandler implements contracts.MeshHandler by echoing the payload.
type echoMeshHandler struct{}

func (echoMeshHandler) HandleCall(_ context.Context, _ string, payload []byte) ([]byte, error) {
	return payload, nil
}
