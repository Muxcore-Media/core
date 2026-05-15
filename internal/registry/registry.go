package registry

import (
	"context"
	"fmt"
	"sync"

	"github.com/Muxcore-Media/core/pkg/contracts"
)

type Registry struct {
	mu      sync.RWMutex
	modules map[string]*Entry
}

type Entry struct {
	Module   contracts.Module
	Info     contracts.ModuleInfo
	State    contracts.ModuleState
	Health   error
	Deps     []string
}

func New() *Registry {
	return &Registry{
		modules: make(map[string]*Entry),
	}
}

func (r *Registry) Register(module contracts.Module, deps []string) error {
	info := module.Info()
	if info.ID == "" {
		return fmt.Errorf("module ID is required")
	}
	if info.Name == "" {
		return fmt.Errorf("module name is required")
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.modules[info.ID]; exists {
		return fmt.Errorf("module %q already registered", info.ID)
	}

	r.modules[info.ID] = &Entry{
		Module: module,
		Info:   info,
		State:  contracts.ModuleStateRegistered,
		Deps:   deps,
	}
	return nil
}

func (r *Registry) Unregister(id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.modules[id]; !exists {
		return fmt.Errorf("module %q not found", id)
	}
	delete(r.modules, id)
	return nil
}

func (r *Registry) Get(id string) (*Entry, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	entry, ok := r.modules[id]
	if !ok {
		return nil, fmt.Errorf("module %q not found", id)
	}
	return entry, nil
}

func (r *Registry) List() []*Entry {
	r.mu.RLock()
	defer r.mu.RUnlock()

	entries := make([]*Entry, 0, len(r.modules))
	for _, e := range r.modules {
		entries = append(entries, e)
	}
	return entries
}

func (r *Registry) ListByKind(kind contracts.ModuleKind) []*Entry {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var entries []*Entry
	for _, e := range r.modules {
		if e.Info.Kind == kind {
			entries = append(entries, e)
		}
	}
	return entries
}

func (r *Registry) Discover(ctx context.Context, kind contracts.ModuleKind) []contracts.ModuleInfo {
	entries := r.ListByKind(kind)
	infos := make([]contracts.ModuleInfo, len(entries))
	for i, e := range entries {
		infos[i] = e.Info
	}
	return infos
}

func (r *Registry) SetState(id string, state contracts.ModuleState) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	entry, ok := r.modules[id]
	if !ok {
		return fmt.Errorf("module %q not found", id)
	}
	entry.State = state
	return nil
}

func (r *Registry) SetHealth(id string, err error) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	entry, ok := r.modules[id]
	if !ok {
		return fmt.Errorf("module %q not found", id)
	}
	entry.Health = err
	return nil
}

func (r *Registry) ResolveDeps(id string) ([]string, error) {
	entry, err := r.Get(id)
	if err != nil {
		return nil, err
	}

	resolved := make(map[string]bool)
	if err := r.resolveDeps(entry, resolved, make(map[string]bool)); err != nil {
		return nil, err
	}

	depList := make([]string, 0, len(resolved))
	for dep := range resolved {
		if dep != id {
			depList = append(depList, dep)
		}
	}
	return depList, nil
}

func (r *Registry) resolveDeps(entry *Entry, resolved map[string]bool, visiting map[string]bool) error {
	id := entry.Info.ID
	if visiting[id] {
		return fmt.Errorf("circular dependency detected: %q", id)
	}
	if resolved[id] {
		return nil
	}

	visiting[id] = true
	resolved[id] = true

	for _, depID := range entry.Deps {
		depEntry, err := r.Get(depID)
		if err != nil {
			return fmt.Errorf("module %q depends on %q which is not registered", id, depID)
		}
		if err := r.resolveDeps(depEntry, resolved, visiting); err != nil {
			return err
		}
	}

	visiting[id] = false
	return nil
}

func (r *Registry) Count() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.modules)
}

// FindByKind returns module entries for the given kind, implementing contracts.ServiceRegistry.
func (r *Registry) FindByKind(kind contracts.ModuleKind) []contracts.ModuleEntry {
	entries := r.ListByKind(kind)
	result := make([]contracts.ModuleEntry, len(entries))
	for i, e := range entries {
		result[i] = contracts.ModuleEntry{Info: e.Info, Module: e.Module}
	}
	return result
}

// Resolve returns a module entry by ID, implementing contracts.ServiceRegistry.
func (r *Registry) Resolve(id string) (contracts.ModuleEntry, error) {
	entry, err := r.Get(id)
	if err != nil {
		return contracts.ModuleEntry{}, err
	}
	return contracts.ModuleEntry{Info: entry.Info, Module: entry.Module}, nil
}

// ListAll returns every registered module, implementing contracts.ServiceRegistry.
func (r *Registry) ListAll() []contracts.ModuleEntry {
	entries := r.List()
	result := make([]contracts.ModuleEntry, len(entries))
	for i, e := range entries {
		result[i] = contracts.ModuleEntry{Info: e.Info, Module: e.Module}
	}
	return result
}
