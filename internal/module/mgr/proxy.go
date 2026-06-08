package mgr

import (
	"context"
	"fmt"
	"os/exec"
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
	info     contracts.ModuleInfo
	exitErr  error       // set when the process exits (nil = running)
	exitMu   sync.RWMutex
}

// NewSidecarProxy creates a registry-compatible proxy for a sidecar module.
func NewSidecarProxy(info contracts.ModuleInfo) *SidecarProxy {
	return &SidecarProxy{info: info}
}

// TrackProcess attaches the running process for health monitoring.
// startErr is the error from cmd.Start() — nil means the process started.
func (p *SidecarProxy) TrackProcess(cmd *exec.Cmd) {
	go func() {
		err := cmd.Wait()
		p.exitMu.Lock()
		p.exitErr = err
		p.exitMu.Unlock()
	}()
}

func (p *SidecarProxy) Info() contracts.ModuleInfo    { return p.info }
func (p *SidecarProxy) Init(_ context.Context) error   { return nil }
func (p *SidecarProxy) Start(_ context.Context) error  { return nil }
func (p *SidecarProxy) Stop(_ context.Context) error   { return nil }

// Health returns nil when the sidecar process is running, or an error
// describing the exit status (crashed, exited with non-zero code, etc.).
// Returns nil for proxies that haven't been attached to a process yet
// (registration order: proxy created before spawn).
func (p *SidecarProxy) Health(_ context.Context) error {
	p.exitMu.RLock()
	defer p.exitMu.RUnlock()
	if p.exitErr != nil {
		return fmt.Errorf("sidecar process exited: %w", p.exitErr)
	}
	return nil
}
