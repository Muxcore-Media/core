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
	schemas map[contracts.MediaType]contracts.MediaTypeSchema
}

type mockEntry struct {
	Info   contracts.ModuleInfo
	Module contracts.Module
}

func NewRegistry() *Registry {
	return &Registry{
		modules: make(map[string]*mockEntry),
		schemas: make(map[contracts.MediaType]contracts.MediaTypeSchema),
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

func (r *Registry) FindByKind(kind contracts.ModuleKind) []contracts.ModuleEntry {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var result []contracts.ModuleEntry
	for _, e := range r.modules {
		for _, k := range e.Info.Kinds {
			if k == kind {
				result = append(result, contracts.ModuleEntry{
					Info: e.Info, Module: e.Module,
				})
				break
			}
		}
	}
	return result
}

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

func (r *Registry) Resolve(id string) (contracts.ModuleEntry, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	e, ok := r.modules[id]
	if !ok {
		return contracts.ModuleEntry{}, fmt.Errorf("module %q not found", id)
	}
	return contracts.ModuleEntry{Info: e.Info, Module: e.Module}, nil
}

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

func (r *Registry) RegisterMediaSchema(schema contracts.MediaTypeSchema) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.schemas[schema.MediaType]; exists {
		return fmt.Errorf("schema already registered for %q", schema.MediaType)
	}
	r.schemas[schema.MediaType] = schema
	return nil
}

func (r *Registry) MediaSchema(mt contracts.MediaType) (contracts.MediaTypeSchema, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	s, ok := r.schemas[mt]
	return s, ok
}

func (r *Registry) MediaSchemas() []contracts.MediaTypeSchema {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var result []contracts.MediaTypeSchema
	for _, s := range r.schemas {
		result = append(result, s)
	}
	return result
}
