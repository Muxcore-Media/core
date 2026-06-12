package mgr

import (
	"context"
	"fmt"
	"testing"

	"github.com/Muxcore-Media/core/pkg/contracts"
)

func TestNewSidecarProxy(t *testing.T) {
	info := contracts.ModuleInfo{
		ID:      "test-module",
		Name:    "Test Module",
		Version: "v1.0.0",
	}
	p := NewSidecarProxy(info)
	got := p.Info()
	if got.ID != info.ID || got.Name != info.Name || got.Version != info.Version {
		t.Errorf("NewSidecarProxy Info() = %+v, want %+v", got, info)
	}
}

func TestSidecarProxy_Health_NoProcess(t *testing.T) {
	p := NewSidecarProxy(contracts.ModuleInfo{ID: "test"})
	if err := p.Health(context.Background()); err != nil {
		t.Errorf("Health() without process = %v, want nil", err)
	}
}

func TestSidecarProxy_Exit_ReportsHealth(t *testing.T) {
	p := NewSidecarProxy(contracts.ModuleInfo{ID: "test"})
	p.setExit(nil)
	if err := p.Health(context.Background()); err != nil {
		t.Errorf("Health() after clean exit = %v, want nil", err)
	}
}

func TestSidecarProxy_Crash_ReportsError(t *testing.T) {
	p := NewSidecarProxy(contracts.ModuleInfo{ID: "test"})
	p.setExit(fmt.Errorf("exit status 1"))
	if err := p.Health(context.Background()); err == nil {
		t.Error("Health() after crash = nil, want error")
	}
}

func TestSidecarProxy_LifecycleMethodsAreNoops(t *testing.T) {
	p := NewSidecarProxy(contracts.ModuleInfo{ID: "test"})
	ctx := context.Background()
	if err := p.Init(ctx); err != nil {
		t.Errorf("Init() = %v, want nil", err)
	}
	if err := p.Start(ctx); err != nil {
		t.Errorf("Start() = %v, want nil", err)
	}
	if err := p.Stop(ctx); err != nil {
		t.Errorf("Stop() = %v, want nil", err)
	}
}
