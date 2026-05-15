package registry

import (
	"context"
	"fmt"
	"reflect"
	"sync"

	"github.com/Muxcore-Media/core/pkg/contracts"
)

type Registry struct {
	mu          sync.RWMutex
	modules     map[string]*Entry
	capIndex    map[string]map[string]bool              // capability -> moduleID -> true
	schemaIndex map[contracts.MediaType]contracts.MediaTypeSchema
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
		modules:     make(map[string]*Entry),
		capIndex:    make(map[string]map[string]bool),
		schemaIndex: make(map[contracts.MediaType]contracts.MediaTypeSchema),
	}
}

// kindInterfaceMap maps each ModuleKind to the required contract interface.
// Kinds not in this map have no required interface and skip validation.
var kindInterfaceMap = map[contracts.ModuleKind]any{
	contracts.ModuleKindAuth:         (*contracts.AuthProvider)(nil),
	contracts.ModuleKindDownloader:   (*contracts.Downloader)(nil),
	contracts.ModuleKindIndexer:      (*contracts.Indexer)(nil),
	contracts.ModuleKindPlayback:     (*contracts.Playback)(nil),
	contracts.ModuleKindMediaManager: (*contracts.MediaLibrary)(nil),
	contracts.ModuleKindWorkflow:     (*contracts.WorkflowEngine)(nil),
	contracts.ModuleKindStorage:      (*contracts.StorageProvider)(nil),
	contracts.ModuleKindScheduler:    (*contracts.Scheduler)(nil),
}

func (r *Registry) Register(module contracts.Module, deps []string) error {
	info := module.Info()
	if info.ID == "" {
		return fmt.Errorf("module ID is required")
	}
	if info.Name == "" {
		return fmt.Errorf("module name is required")
	}

	// Validate that for each declared kind, the module implements the required interface.
	for _, kind := range info.Kinds {
		if iface, ok := kindInterfaceMap[kind]; ok {
			// iface is a nil pointer to an interface type; dereference to get the interface type
			ifaceType := reflect.TypeOf(iface).Elem()
			if !reflect.TypeOf(module).Implements(ifaceType) {
				return fmt.Errorf("module %q claims kind %q but does not implement the required interface", info.ID, kind)
			}
		}
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

	// Populate capability index.
	for _, cap := range info.Capabilities {
		if r.capIndex[cap] == nil {
			r.capIndex[cap] = make(map[string]bool)
		}
		r.capIndex[cap][info.ID] = true
	}

	return nil
}

func (r *Registry) Unregister(id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	entry, exists := r.modules[id]
	if !exists {
		return fmt.Errorf("module %q not found", id)
	}

	// Clean up capability index.
	for _, cap := range entry.Info.Capabilities {
		if mods, ok := r.capIndex[cap]; ok {
			delete(mods, id)
			if len(mods) == 0 {
				delete(r.capIndex, cap)
			}
		}
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
		for _, k := range e.Info.Kinds {
			if k == kind {
				entries = append(entries, e)
				break
			}
		}
	}
	return entries
}

func (r *Registry) ListByCapability(cap string) []*Entry {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if mods, ok := r.capIndex[cap]; ok {
		entries := make([]*Entry, 0, len(mods))
		for id := range mods {
			if e, exists := r.modules[id]; exists {
				entries = append(entries, e)
			}
		}
		return entries
	}
	return nil
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
		result[i] = contracts.ModuleEntry{Info: e.Info, State: e.State, Module: e.Module}
	}
	return result
}

// FindByCapability returns module entries that declare the given capability, implementing contracts.ServiceRegistry.
func (r *Registry) FindByCapability(cap string) []contracts.ModuleEntry {
	entries := r.ListByCapability(cap)
	result := make([]contracts.ModuleEntry, len(entries))
	for i, e := range entries {
		result[i] = contracts.ModuleEntry{Info: e.Info, State: e.State, Module: e.Module}
	}
	return result
}

// SupportsCapability checks whether a specific module supports the given capability, implementing contracts.ServiceRegistry.
func (r *Registry) SupportsCapability(moduleID, cap string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if mods, ok := r.capIndex[cap]; ok {
		return mods[moduleID]
	}
	return false
}

// RegisterMediaSchema registers a metadata schema for a media type, implementing contracts.ServiceRegistry.
func (r *Registry) RegisterMediaSchema(schema contracts.MediaTypeSchema) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.schemaIndex[schema.MediaType]; exists {
		return fmt.Errorf("schema already registered for media type %q", schema.MediaType)
	}
	r.schemaIndex[schema.MediaType] = schema
	return nil
}

// MediaSchema returns the schema for a media type, implementing contracts.ServiceRegistry.
func (r *Registry) MediaSchema(mediaType contracts.MediaType) (contracts.MediaTypeSchema, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	schema, ok := r.schemaIndex[mediaType]
	return schema, ok
}

// MediaSchemas returns all registered media type schemas, implementing contracts.ServiceRegistry.
func (r *Registry) MediaSchemas() []contracts.MediaTypeSchema {
	r.mu.RLock()
	defer r.mu.RUnlock()

	schemas := make([]contracts.MediaTypeSchema, 0, len(r.schemaIndex))
	for _, s := range r.schemaIndex {
		schemas = append(schemas, s)
	}
	return schemas
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
		result[i] = contracts.ModuleEntry{Info: e.Info, State: e.State, Module: e.Module}
	}
	return result
}
