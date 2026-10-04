package main

import (
	"context"
	"flag"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc"

	"github.com/Muxcore-Media/core/internal/api"
	"github.com/Muxcore-Media/core/internal/audit"
	"github.com/Muxcore-Media/core/internal/config"
	"github.com/Muxcore-Media/core/internal/events"
	"github.com/Muxcore-Media/core/internal/grpcmesh"
	modlifecycle "github.com/Muxcore-Media/core/internal/module"
	modulemgr "github.com/Muxcore-Media/core/internal/module/mgr"
	"github.com/Muxcore-Media/core/internal/registry"
	"github.com/Muxcore-Media/core/internal/storage"
	"github.com/Muxcore-Media/core/internal/workerpool"
	"github.com/Muxcore-Media/core/pkg/contracts"
)

func TestParseFlags(t *testing.T) {
	oldFS := flag.CommandLine
	oldArgs := os.Args
	t.Cleanup(func() {
		flag.CommandLine = oldFS
		os.Args = oldArgs
	})

	flag.CommandLine = flag.NewFlagSet("muxcored-test", flag.ContinueOnError)
	os.Args = []string{"muxcored", "-tag", "media", "-dry-run", "-idempotency-dir", "/tmp/idemp"}
	tag, spool, watchdog, taskDir, idempDir, dlDir, ver, dry := parseFlags()
	if *tag != "media" {
		t.Fatalf("tag=%q", *tag)
	}
	if *spool == "" {
		t.Fatal("expected default spool URL")
	}
	if *watchdog != "" || *taskDir != "" {
		t.Fatal("expected empty optional paths")
	}
	if *idempDir != "/tmp/idemp" {
		t.Fatalf("idempotency-dir=%q", *idempDir)
	}
	if *dlDir != "" {
		t.Fatal("expected empty deadletter-dir")
	}
	if *ver {
		t.Fatal("version should be false")
	}
	if !*dry {
		t.Fatal("dry-run should be true")
	}
}

func TestSetupContext(t *testing.T) {
	ctx, cancel, sighup := setupContext()
	defer cancel()
	if ctx == nil || sighup == nil {
		t.Fatal("expected context and sighup channel")
	}
	select {
	case <-ctx.Done():
		t.Fatal("context should not be done yet")
	default:
	}
	cancel()
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("context did not cancel")
	}
}

func TestInitStoresMemory(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	reg := registry.New()

	idemp := initIdempotency(ctx, reg, "node-1", "")
	if idemp == nil {
		t.Fatal("idempotency")
	}
	dl := initDeadLetter(ctx, reg, "node-1", "")
	if dl == nil {
		t.Fatal("deadletter")
	}
	es := initEventStore(reg, "node-1")
	if es == nil {
		t.Fatal("eventstore")
	}
	rp := initRetry(reg, "node-1")
	if rp == nil {
		t.Fatal("retry")
	}
	bus := initEventBus()
	if bus == nil {
		t.Fatal("event bus")
	}
}

func TestInitStoresPersistentDirs(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	reg := registry.New()
	root := t.TempDir()

	idemp := initIdempotency(ctx, reg, "node-1", filepath.Join(root, "idemp"))
	if idemp == nil {
		t.Fatal("idempotency dir")
	}
	dl := initDeadLetter(ctx, reg, "node-1", filepath.Join(root, "dl"))
	if dl == nil {
		t.Fatal("deadletter dir")
	}
}

func TestWarnEncryptionProviders_None(t *testing.T) {
	warnEncryptionProviders(registry.New())
}

type stubEncModule struct {
	info      contracts.ModuleInfo
	available bool
}

func (m *stubEncModule) Info() contracts.ModuleInfo     { return m.info }
func (m *stubEncModule) Init(context.Context) error     { return nil }
func (m *stubEncModule) Start(context.Context) error    { return nil }
func (m *stubEncModule) Stop(context.Context) error     { return nil }
func (m *stubEncModule) Health(context.Context) error   { return nil }
func (m *stubEncModule) Encrypt(ctx context.Context, plaintext []byte) ([]byte, error) {
	return plaintext, nil
}
func (m *stubEncModule) Decrypt(ctx context.Context, ciphertext []byte) ([]byte, error) {
	return ciphertext, nil
}
func (m *stubEncModule) Available() bool                  { return m.available }
func (m *stubEncModule) RotateKey(ctx context.Context) error { return nil }

func TestWarnEncryptionProviders_Available(t *testing.T) {
	reg := registry.New()
	mod := &stubEncModule{
		info: contracts.ModuleInfo{
			ID: "enc", Name: "Enc", Version: "1.0.0",
			Capabilities: []string{contracts.CapabilityEncryption},
		},
		available: true,
	}
	if err := reg.Register(mod, nil); err != nil {
		t.Fatal(err)
	}
	warnEncryptionProviders(reg)
}

func TestWarnEncryptionProviders_Unavailable(t *testing.T) {
	reg := registry.New()
	mod := &stubEncModule{
		info: contracts.ModuleInfo{
			ID: "enc-down", Name: "Enc", Version: "1.0.0",
			Capabilities: []string{contracts.CapabilityEncryption},
		},
		available: false,
	}
	if err := reg.Register(mod, nil); err != nil {
		t.Fatal(err)
	}
	warnEncryptionProviders(reg)
}

func TestWirePoliciesAndAuth_EmptyRegistry(t *testing.T) {
	reg := registry.New()
	meshSrv := grpcmesh.NewServer()
	meshClient := grpcmesh.NewClient(meshSrv)
	bus := events.NewMemoryBus()
	srv := api.NewServer(":0", "", "")
	auth := grpcmesh.NewAuthInterceptor()
	storageGrpc := grpcmesh.NewStorageServer(nil)

	wirePoliciesAndAuth(reg, meshClient, storageGrpc, bus, srv, auth, nil, 32<<20)
}

func TestInitStorageAndHTTPAndAuditAndModuleMgr(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cfg := config.Default()
	cfg.Server.Addr = ":0"
	cfg.GRPC.Addr = ":0"
	cfg.Audit.Path = filepath.Join(t.TempDir(), "audit.log")
	cfg.Audit.MaxSizeMB = 1
	cfg.Audit.MaxRotatedFiles = 2
	cfg.Spool.AllowedHosts = []string{"github.com"}

	reg := registry.New()
	bus := events.NewMemoryBus()
	t.Setenv("MUXCORE_STORAGE_DIR", filepath.Join(t.TempDir(), "storage"))
	t.Setenv("MUXCORE_METRICS_ENABLE", "true")
	t.Setenv("MUXCORE_DEBUG_ENABLE", "1")

	store, watchCancel := initStorage(ctx, cfg, reg, bus, nil, 0)
	if store == nil || watchCancel == nil {
		t.Fatal("initStorage")
	}
	defer watchCancel()

	meshSrv := grpcmesh.NewServer()
	meshClient := grpcmesh.NewClient(meshSrv)
	discovery := grpcmesh.NewDiscoveryServer("node-test", ":0", ":0", "", func() ([]string, map[string]string) {
		return nil, nil
	})
	pool := grpcmesh.NewConnPool()
	wp := workerpool.New("node-test")

	srv, metrics := initHTTPServer(cfg, reg, bus, store, meshClient, discovery, pool, wp)
	if srv == nil {
		t.Fatal("initHTTPServer")
	}
	if metrics == nil {
		t.Fatal("expected metrics provider when MUXCORE_METRICS_ENABLE=true")
	}
	_ = metrics.ModuleDegradedCount()
	_ = metrics.AllocBytes()

	auditLogger := initAudit(cfg, bus, store, srv)
	if auditLogger == nil {
		t.Fatal("initAudit")
	}

	grpcSrv := grpc.NewServer()
	auth := grpcmesh.NewAuthInterceptor()
	modMgr, lifeMgr := initModuleManager(cfg, reg, bus, auditLogger, grpcSrv, metrics, "/bin/false", auth, nil)
	if modMgr == nil || lifeMgr == nil {
		t.Fatal("initModuleManager")
	}
	if metrics.ModuleSpawnCount == nil || metrics.ModuleRestartCount == nil || metrics.ModuleResolveCount == nil {
		t.Fatal("expected metrics hooks from module manager")
	}
}

func TestInitHTTPServer_NoExtras(t *testing.T) {
	t.Setenv("MUXCORE_METRICS_ENABLE", "")
	t.Setenv("MUXCORE_DEBUG_ENABLE", "")
	cfg := config.Default()
	cfg.Server.Addr = ":0"
	reg := registry.New()
	bus := events.NewMemoryBus()
	store := storage.NewOrchestrator(reg)
	meshClient := grpcmesh.NewClient(grpcmesh.NewServer())
	discovery := grpcmesh.NewDiscoveryServer("n", ":0", ":0", "", func() ([]string, map[string]string) { return nil, nil })
	srv, metrics := initHTTPServer(cfg, reg, bus, store, meshClient, discovery, grpcmesh.NewConnPool(), nil)
	if srv == nil {
		t.Fatal("server")
	}
	if metrics != nil {
		t.Fatal("expected nil metrics when disabled")
	}
}

func TestInitEventBus_WithWAL(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.wal")
	t.Setenv("MUXCORE_EVENT_JOURNAL_PATH", path)
	bus := initEventBus()
	if bus == nil {
		t.Fatal("bus")
	}
}

func TestInitGRPCMesh_AutoMTLS(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cfg := config.Default()
	cfg.GRPC.Addr = ":0"
	cfg.Server.Addr = ":0"
	cfg.GRPC.MTLSEnabled = true
	cfg.GRPC.CertFile = ""
	cfg.GRPC.KeyFile = ""
	cfg.GRPC.CACertDir = t.TempDir()
	cfg.GRPC.MaxMessageSizeMB = 0 // exercise default 32MB branch

	bus := events.NewMemoryBus()
	grpcSrv, meshClient, discovery, pool, reg, creds, auth, nodeID, cluster, certAuth := initGRPCMesh(ctx, cfg, bus)
	if grpcSrv == nil || meshClient == nil || discovery == nil || pool == nil || reg == nil {
		t.Fatal("expected mesh components")
	}
	if creds == nil {
		t.Fatal("expected TLS creds")
	}
	if auth == nil || cluster == nil || certAuth == nil {
		t.Fatal("expected auth/cluster/ca")
	}
	if nodeID == "" {
		t.Fatal("expected node id")
	}
	if cfg.GRPC.CertFile == "" || cfg.GRPC.KeyFile == "" || cfg.GRPC.CACertFile == "" {
		t.Fatal("expected auto-issued cert paths on cfg")
	}
	if _, err := os.Stat(cfg.GRPC.CertFile); err != nil {
		t.Fatalf("server cert missing: %v", err)
	}
	if cfg.Server.CertFile == "" {
		t.Fatal("expected HTTP cert filled from auto CA")
	}
	_ = cluster.Stop(ctx)
	grpcSrv.Stop()
}

func TestInitGRPCMesh_InsecureDevSkip(t *testing.T) {
	t.Setenv("MUXCORE_DEV_TLS_SKIP", "true")
	t.Setenv("MUXCORE_INSECURE_DISABLE_TLS", "")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cfg := config.Default()
	cfg.GRPC.Addr = ":0"
	cfg.Server.Addr = ":0"
	cfg.GRPC.MTLSEnabled = false
	cfg.GRPC.CertFile = ""
	cfg.GRPC.KeyFile = ""
	cfg.GRPC.CACertDir = ""

	bus := events.NewMemoryBus()
	grpcSrv, _, _, _, _, creds, _, _, cluster, certAuth := initGRPCMesh(ctx, cfg, bus)
	if grpcSrv == nil {
		t.Fatal("grpc server")
	}
	if creds != nil {
		t.Fatal("expected nil creds in insecure mode")
	}
	if certAuth != nil {
		t.Fatal("expected no CA without mtls/ca dir")
	}
	_ = cluster.Stop(ctx)
	grpcSrv.Stop()
}

func TestRegisterManagementGRPC(t *testing.T) {
	cfg := config.Default()
	cfg.Audit.Path = filepath.Join(t.TempDir(), "audit.log")
	reg := registry.New()
	bus := events.NewMemoryBus()
	auditLogger, err := audit.NewFileLogger(cfg.Audit.Path)
	if err != nil {
		t.Fatal(err)
	}
	life := modlifecycle.NewManager(reg, bus)
	modMgr := modulemgr.NewManager(":0", reg, life)
	discovery := grpcmesh.NewDiscoveryServer("n1", ":0", ":0", "", func() ([]string, map[string]string) {
		return nil, nil
	})
	grpcSrv := grpc.NewServer()
	var mu sync.Mutex
	registerManagementGRPC(grpcSrv, "file:///tmp/spool", cfg, &mu, modMgr, reg, "n1", discovery, auditLogger)
	if len(grpcSrv.GetServiceInfo()) < 3 {
		t.Fatalf("expected management services registered, got %d", len(grpcSrv.GetServiceInfo()))
	}
}

func TestStartHTTPAndGRPCAndShutdown(t *testing.T) {
	t.Setenv("MUXCORE_DEV_TLS_SKIP", "true")
	cfg := config.Default()
	cfg.Server.Addr = "127.0.0.1:0"
	cfg.GRPC.Addr = "127.0.0.1:0"

	// Bind HTTP to an ephemeral port via Listen first by using NewServer then replacing — api.Server uses cfg addr as-is.
	// Pick free ports.
	lnHTTP, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	httpAddr := lnHTTP.Addr().String()
	_ = lnHTTP.Close()
	lnGRPC, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	grpcAddr := lnGRPC.Addr().String()
	_ = lnGRPC.Close()

	cfg.Server.Addr = httpAddr
	cfg.GRPC.Addr = grpcAddr

	reg := registry.New()
	bus := events.NewMemoryBus()
	store := storage.NewOrchestrator(reg)
	srv := api.NewServer(cfg.Server.Addr, "", "")
	grpcSrv := grpc.NewServer()

	ctx, cancel := context.WithCancel(context.Background())
	sighup := make(chan os.Signal, 1)
	fatalErr := startHTTPAndGRPC(cfg, srv, grpcSrv)

	// Give listeners a moment
	time.Sleep(50 * time.Millisecond)

	watchCancel := func() {}
	life := modlifecycle.NewManager(reg, bus)
	modMgr := modulemgr.NewManager(cfg.GRPC.Addr, reg, life)
	pool := grpcmesh.NewConnPool()
	wp := workerpool.New("n")
	discovery := grpcmesh.NewDiscoveryServer("n", cfg.GRPC.Addr, cfg.Server.Addr, "", func() ([]string, map[string]string) {
		return nil, nil
	})

	done := make(chan struct{})
	go func() {
		awaitAndShutdown(ctx, cancel, sighup, fatalErr, srv, grpcSrv, watchCancel, wp, bus, modMgr, pool, discovery)
		close(done)
	}()
	cancel()
	select {
	case <-done:
	case <-time.After(20 * time.Second):
		t.Fatal("shutdown timed out")
	}
	_ = store
}

func TestWaitForSidecarPolicies_EmptyFast(t *testing.T) {
	oldA, oldS := sidecarPolicyAttempts, sidecarPolicySleep
	sidecarPolicyAttempts = 1
	sidecarPolicySleep = 0
	t.Cleanup(func() {
		sidecarPolicyAttempts = oldA
		sidecarPolicySleep = oldS
	})

	reg := registry.New()
	meshClient := grpcmesh.NewClient(grpcmesh.NewServer())
	bus := events.NewMemoryBus()
	srv := api.NewServer(":0", "", "")
	auth := grpcmesh.NewAuthInterceptor()
	storageGrpc := grpcmesh.NewStorageServer(nil)
	waitForSidecarPolicies(reg, meshClient, storageGrpc, bus, srv, auth, nil, 32<<20)
}

func TestResolveAddr(t *testing.T) {
	if got := resolveAddr("10.0.0.5:9090"); got != "10.0.0.5:9090" {
		t.Errorf("addr with host must be unchanged, got %q", got)
	}
	if got := resolveAddr("not-an-addr"); got != "not-an-addr" {
		t.Errorf("unparseable addr must be unchanged, got %q", got)
	}
	got := resolveAddr(":9090")
	host, port, err := net.SplitHostPort(got)
	if err != nil || port != "9090" {
		t.Fatalf("bare port should resolve to host:9090, got %q (err %v)", got, err)
	}
	if h := resolveHost(); h != "" && host != h {
		t.Errorf("expected host %q, got %q", h, host)
	}
}
