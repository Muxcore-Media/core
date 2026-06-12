package mgr

import (
	"context"
	"fmt"
	"sync"

	"github.com/Muxcore-Media/core/pkg/contracts"
)

// SidecarProxy wraps a sidecar module's ModuleInfo so it satisfies
// contracts.Module for registry registration. The real module runs in a
// separate process — lifecycle methods here are no-ops because the sidecar
// process manages its own lifecycle.
//
// Health is tracked via the sidecar process's exit status: after Spawn(),
// TrackProcess attaches the exec.Cmd so Health() can report the actual
// process state (running, exited, or crashed).
type SidecarProxy struct {
	info    contracts.ModuleInfo
	exitErr error
	exitMu  sync.RWMutex
}

// NewSidecarProxy creates a registry-compatible proxy for a sidecar module.
func NewSidecarProxy(info contracts.ModuleInfo) *SidecarProxy {
	return &SidecarProxy{info: info}
}

// TrackProcess is deprecated and unused. Use Manager.watchProcess /
// proxy.setExit instead. Do not call — the goroutine races with setExit.

func (p *SidecarProxy) Info() contracts.ModuleInfo    { return p.info }
func (p *SidecarProxy) Init(_ context.Context) error  { return nil }
func (p *SidecarProxy) Start(_ context.Context) error { return nil }
func (p *SidecarProxy) Stop(_ context.Context) error  { return nil }

// Health returns nil when the sidecar process is running, or an error
// describing the exit status (crashed, exited with non-zero code, etc.).
// Returns nil for proxies that haven't been attached to a process yet
// (registration order: proxy created before spawn).
// setExit records the exit status of the tracked process.
// Called by Manager.watchProcess — the sole owner of cmd.Wait().
func (p *SidecarProxy) setExit(err error) {
	p.exitMu.Lock()
	p.exitErr = err
	p.exitMu.Unlock()
}

func (p *SidecarProxy) Health(_ context.Context) error {
	p.exitMu.RLock()
	defer p.exitMu.RUnlock()
	if p.exitErr != nil {
		return fmt.Errorf("sidecar process exited: %w", p.exitErr)
	}
	return nil
}
