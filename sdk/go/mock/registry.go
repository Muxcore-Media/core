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
