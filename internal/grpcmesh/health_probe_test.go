package grpcmesh

import (
	"context"
	"os"
	"testing"

	"google.golang.org/grpc"
)

func TestIsDevTLSSkip_True(t *testing.T) {
	os.Setenv("MUXCORE_DEV_TLS_SKIP", "true")
	defer os.Unsetenv("MUXCORE_DEV_TLS_SKIP")
	if !isDevTLSSkip() {
		t.Error("expected isDevTLSSkip()=true when env is 'true'")
	}
}

func TestIsDevTLSSkip_One(t *testing.T) {
	os.Setenv("MUXCORE_DEV_TLS_SKIP", "1")
	defer os.Unsetenv("MUXCORE_DEV_TLS_SKIP")
	if !isDevTLSSkip() {
		t.Error("expected isDevTLSSkip()=true when env is '1'")
	}
}

func TestIsDevTLSSkip_False(t *testing.T) {
	os.Setenv("MUXCORE_DEV_TLS_SKIP", "false")
	defer os.Unsetenv("MUXCORE_DEV_TLS_SKIP")
	if isDevTLSSkip() {
		t.Error("expected isDevTLSSkip()=false when env is 'false'")
	}
}

func TestIsDevTLSSkip_Unset(t *testing.T) {
	os.Unsetenv("MUXCORE_DEV_TLS_SKIP")
	if isDevTLSSkip() {
		t.Error("expected isDevTLSSkip()=false when env is unset")
	}
}

func TestRegisterStandardHealthProbe(t *testing.T) {
	srv := grpc.NewServer()
	defer srv.Stop()
	RegisterStandardHealthProbe(srv)
}

func TestStandardHealthProbe_Check_Serving(t *testing.T) {
	probe := &standardHealthProbe{}
	resp, err := probe.Check(context.Background(), nil)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if resp.GetStatus().String() != "SERVING" {
		t.Errorf("expected SERVING, got %s", resp.GetStatus().String())
	}
}
