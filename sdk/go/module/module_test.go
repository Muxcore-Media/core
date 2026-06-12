package module

import (
	"context"
	"os"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/Muxcore-Media/core/pkg/contracts"
)

type testModule struct {
	contracts.Module
	info contracts.ModuleInfo
}

func (m *testModule) Info() contracts.ModuleInfo     { return m.info }
func (m *testModule) Init(_ context.Context) error   { return nil }
func (m *testModule) Start(_ context.Context) error  { return nil }
func (m *testModule) Stop(_ context.Context) error   { return nil }
func (m *testModule) Health(_ context.Context) error { return nil }

func TestResolveString_Explicit(t *testing.T) {
	got := resolveString("explicit", "MUXCORE_TEST_ENV", "", "fallback")
	if got != "explicit" {
		t.Fatalf("expected explicit, got %q", got)
	}
}

func TestResolveString_EnvVar(t *testing.T) {
	os.Setenv("MUXCORE_TEST_ENV", "from-env")
	defer os.Unsetenv("MUXCORE_TEST_ENV")

	got := resolveString("", "MUXCORE_TEST_ENV", "", "fallback")
	if got != "from-env" {
		t.Fatalf("expected from-env, got %q", got)
	}
}

func TestResolveString_Fallback(t *testing.T) {
	got := resolveString("", "MUXCORE_NONEXISTENT_ENV", "", "fallback")
	if got != "fallback" {
		t.Fatalf("expected fallback, got %q", got)
	}
}

func TestResolveString_FlagWithDefault(t *testing.T) {
	got := resolveString("", "", "", "default-val")
	if got != "default-val" {
		t.Fatalf("expected default-val, got %q", got)
	}
}

func TestConfig_ModuleID_FallsBackToInfo(t *testing.T) {
	mod := &testModule{info: contracts.ModuleInfo{ID: "my-module"}}
	got := resolveString("", envModuleID, flagModuleID, mod.Info().ID)
	if got != "my-module" {
		t.Fatalf("expected my-module, got %q", got)
	}
}

func TestConfig_RequiresGRPCAddr(t *testing.T) {
	err := Run(Config{
		Module: &testModule{info: contracts.ModuleInfo{ID: "test"}},
	})
	if err == nil {
		t.Fatal("expected error when no gRPC address is set")
	}
}

func TestConnect_RequiresGRPCAddr(t *testing.T) {
	_, err := Connect(ConnectConfig{})
	if err == nil {
		t.Fatal("expected error when no gRPC address is set")
	}
}

func TestConnect_Success(t *testing.T) {
	// grpc.NewClient is lazy, so this succeeds without a server.
	conn, err := Connect(ConnectConfig{
		GRPCAddr: "localhost:19999",
		Insecure: true,
	})
	if err != nil {
		t.Fatalf("Connect failed: %v", err)
	}
	conn.Close()
}

func TestGRPCDial_WithInsecure(t *testing.T) {
	conn, err := dialGRPC("localhost:19999", true)
	if err != nil {
		t.Fatalf("dialGRPC failed: %v", err)
	}
	conn.Close()
}

func TestGRPCDial_RequiresTLSByDefault(t *testing.T) {
	// Without plaintext, gRPC requires transport security.
	_, err := dialGRPC("localhost:19999", false)
	if err == nil {
		t.Fatal("expected error when no transport security is set")
	}
}

func TestGRPCDial_OptionOrder(t *testing.T) {
	opts := []grpc.DialOption{
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	}
	conn, err := grpc.NewClient("localhost:19999", opts...)
	if err != nil {
		t.Fatalf("grpc.NewClient failed: %v", err)
	}
	conn.Close()
}
