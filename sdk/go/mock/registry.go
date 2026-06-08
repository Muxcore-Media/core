package mock

import (
	"fmt"
	"sync"

	"github.com/Muxcore-Media/core/pkg/contracts"
)

// Registry is a mock service registry for module testing.
type Registry struct {
	mu      sync.RWMutex
	modules map[string]*mockEntry
}

type mockEntry struct {
	Info   contracts.ModuleInfo
	Module contracts.Module
}

// NewRegistry creates an empty mock registry.
func NewRegistry() *Registry {
	return &Registry{
		modules: make(map[string]*mockEntry),
	}
}

// RegisterModule adds a module to the mock registry. Pass a nil Module if
// the test only cares about Info being discoverable.
func (r *Registry) RegisterModule(module contracts.Module) error {
	info := module.Info()
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.modules[info.ID]; exists {
		return fmt.Errorf("module %q already registered", info.ID)
	}
	r.modules[info.ID] = &mockEntry{Info: info, Module: module}
	return nil
}

// FindByRole returns module entries matching the given role string.
func (r *Registry) FindByRole(role string) []contracts.ModuleEntry {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var result []contracts.ModuleEntry
	for _, e := range r.modules {
		for _, k := range e.Info.Roles {
			if k == role {
				result = append(result, contracts.ModuleEntry{
					Info: e.Info, Module: e.Module,
				})
				break
			}
		}
	}
	return result
}

// FindByCapability returns module entries that declare the given capability.
func (r *Registry) FindByCapability(cap string) []contracts.ModuleEntry {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var result []contracts.ModuleEntry
	for _, e := range r.modules {
		for _, c := range e.Info.Capabilities {
			if c == cap {
				result = append(result, contracts.ModuleEntry{
					Info: e.Info, Module: e.Module,
				})
				break
			}
		}
	}
	return result
}

// SupportsCapability checks whether a module supports the given capability.
func (r *Registry) SupportsCapability(moduleID, cap string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	e, ok := r.modules[moduleID]
	if !ok {
		return false
	}
	for _, c := range e.Info.Capabilities {
		if c == cap {
			return true
		}
	}
	return false
}

// StartupOrder returns all registered module IDs in registration order.
// The mock does not enforce DependsOn ordering — tests that require
// dependency ordering should use the real registry implementation.
func (r *Registry) StartupOrder() ([]string, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	ids := make([]string, 0, len(r.modules))
	for id := range r.modules {
		ids = append(ids, id)
	}
	return ids, nil
}

// DependencyGraph returns an empty slice — the mock registry does not
// track DependsOn relationships. Tests requiring dependency graph
// queries should use the real registry implementation.
func (r *Registry) DependencyGraph(id string) ([]string, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if _, ok := r.modules[id]; !ok {
		return nil, fmt.Errorf("module %q not found", id)
	}
	return nil, ErrNotImplemented
}

// Resolve returns a module entry by ID.
func (r *Registry) Resolve(id string) (contracts.ModuleEntry, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	e, ok := r.modules[id]
	if !ok {
		return contracts.ModuleEntry{}, fmt.Errorf("module %q not found", id)
	}
	return contracts.ModuleEntry{Info: e.Info, Module: e.Module}, nil
}

// ListAll returns every registered module.
func (r *Registry) ListAll() []contracts.ModuleEntry {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var result []contracts.ModuleEntry
	for _, e := range r.modules {
		result = append(result, contracts.ModuleEntry{
			Info: e.Info, Module: e.Module,
		})
	}
	return result
}

// ErrNotImplemented is returned by mock methods that intentionally
// do not implement the full contract. Callers should either test against
// the real implementation or provide their own mock.
var ErrNotImplemented = fmt.Errorf("not implemented in mock registry")
