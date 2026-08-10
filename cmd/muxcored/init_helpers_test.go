package main

import (
	"context"
	"flag"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Muxcore-Media/core/internal/api"
	"github.com/Muxcore-Media/core/internal/events"
	"github.com/Muxcore-Media/core/internal/grpcmesh"
	"github.com/Muxcore-Media/core/internal/registry"
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
