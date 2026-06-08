package mgr

import (
	"context"

	"github.com/Muxcore-Media/core/pkg/contracts"
)

// SidecarProxy wraps a sidecar module's ModuleInfo so it satisfies
// contracts.Module for registry registration. The real module runs in a
// separate process — lifecycle methods here are no-ops because the sidecar
// process manages its own lifecycle.
type SidecarProxy struct {
	info contracts.ModuleInfo
}

// NewSidecarProxy creates a registry-compatible proxy for a sidecar module.
func NewSidecarProxy(info contracts.ModuleInfo) *SidecarProxy {
	return &SidecarProxy{info: info}
}

func (p *SidecarProxy) Info() contracts.ModuleInfo    { return p.info }
func (p *SidecarProxy) Init(_ context.Context) error   { return nil }
func (p *SidecarProxy) Start(_ context.Context) error  { return nil }
func (p *SidecarProxy) Stop(_ context.Context) error   { return nil }
func (p *SidecarProxy) Health(_ context.Context) error { return nil }
