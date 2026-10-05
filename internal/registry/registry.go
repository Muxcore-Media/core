//nolint:govet // struct field alignment
package registry

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"sync"

	"github.com/Muxcore-Media/core/pkg/contracts"
)

// Registry is the core module registry. It stores modules indexed by ID,
// maintains a capability index for fast lookup, and provides the
// contracts.Registry interface for module discovery.
type Registry struct {
	mu       sync.RWMutex
	modules  map[string]*Entry
	capIndex map[string]map[string]bool // capability -> moduleID -> true
	// nextSeq is the registration sequence counter (see Entry.seq).
	nextSeq uint64
}

// ErrAlreadyRegistered is returned (wrapped) when a module ID is already
// registered and the registration does not ask for a replace.
var ErrAlreadyRegistered = errors.New("already registered")

// ErrExclusiveConflict is returned (wrapped) by RegisterWith when the module
// declares an exclusive capability that another module ID already provides.
var ErrExclusiveConflict = errors.New("exclusive capability already provided")

// RegisterOptions controls RegisterWith.
type RegisterOptions struct {
	// Replace atomically replaces an existing entry with the same ID instead
	// of failing with ErrAlreadyRegistered. The replacement keeps the old
	// entry's registration order, so it stays the provider of record for its
	// capabilities (ADR-0018: the same verified ID re-registers).
	Replace bool
	// Exclusive lists capabilities that may have only one provider. When the
	// module declares one of them and a module with a different ID already
	// provides it, RegisterWith fails with ErrExclusiveConflict and registers
	// nothing.
	Exclusive []string
}

// Entry holds a registered module and its metadata.
type Entry struct {
	Module contracts.Module
	Info   contracts.ModuleInfo
	State  contracts.ModuleState
	Health error
	Deps   []string
	// seq is the registration order: lower registered earlier. A replace
	// keeps the original value. Listing methods sort by it (then by ID), so
	// the first-registered provider of a capability is always listed first.
	seq uint64
}

// New creates an empty Registry.
func New() *Registry {
	return &Registry{
		modules:  make(map[string]*Entry),
		capIndex: make(map[string]map[string]bool),
	}
}

// Register adds a module to the registry. Returns an error if the module ID
// is empty, the module name is empty, or the ID is already registered
// (wrapping ErrAlreadyRegistered).
// Core performs no interface validation — that responsibility belongs to
// consumer modules, contract repos, and the marketplace compatibility checker.
func (r *Registry) Register(module contracts.Module, deps []string) error {
	_, err := r.RegisterWith(module, deps, RegisterOptions{})
	return err
}

// RegisterWith is Register with options. It reports whether an existing
// entry was replaced (opts.Replace). All checks and the update happen under
// one lock, so concurrent registrations cannot both claim an exclusive
// capability and a replace is never observed half-done.
func (r *Registry) RegisterWith(module contracts.Module, deps []string, opts RegisterOptions) (replaced bool, err error) {
	info := module.Info()
	if info.ID == "" {
		return false, fmt.Errorf("module ID is required")
	}
	if info.Name == "" {
		return false, fmt.Errorf("module name is required")
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	old, exists := r.modules[info.ID]
	if exists && !opts.Replace {
		return false, fmt.Errorf("module %q %w", info.ID, ErrAlreadyRegistered)
	}

	for _, capability := range opts.Exclusive {
		if !hasString(info.Capabilities, capability) {
			continue
		}
		for id := range r.capIndex[capability] {
			if id != info.ID {
				return false, fmt.Errorf("%w: %q is provided by %q, refusing %q",
					ErrExclusiveConflict, capability, id, info.ID)
			}
		}
	}

	seq := r.nextSeq
	if exists {
		seq = old.seq
		r.removeCapsLocked(info.ID, old.Info.Capabilities)
	} else {
		r.nextSeq++
	}

	r.modules[info.ID] = &Entry{
		Module: module,
		Info:   info,
		State:  contracts.ModuleStateRegistered,
		Deps:   deps,
		seq:    seq,
	}

	// Populate capability index.
	for _, cap := range info.Capabilities {
		if r.capIndex[cap] == nil {
			r.capIndex[cap] = make(map[string]bool)
		}
		r.capIndex[cap][info.ID] = true
	}

	slog.Debug("registry: module registered",
		"id", info.ID,
		"name", info.Name,
		"version", info.Version,
		"capabilities", info.Capabilities,
		"replaced", exists,
	)
	return exists, nil
}

func hasString(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// removeCapsLocked removes id from the capability index for caps. Must be
// called with r.mu held for writing.
func (r *Registry) removeCapsLocked(id string, caps []string) {
	for _, capability := range caps {
		if mods, ok := r.capIndex[capability]; ok {
			delete(mods, id)
			if len(mods) == 0 {
				delete(r.capIndex, capability)
			}
		}
	}
}

// sortEntries orders entries by registration order, then by ID.
func sortEntries(entries []*Entry) {
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].seq != entries[j].seq {
			return entries[i].seq < entries[j].seq
		}
		return entries[i].Info.ID < entries[j].Info.ID
	})
}

// Unregister removes a module and cleans up its capability index entries.
func (r *Registry) Unregister(id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	entry, exists := r.modules[id]
	if !exists {
		return fmt.Errorf("module %q not found", id)
	}

	// Clean up capability index.
	r.removeCapsLocked(id, entry.Info.Capabilities)

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
	sortEntries(entries)
	return entries
}

func (r *Registry) ListByRole(role string) []*Entry {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var entries []*Entry
	for _, e := range r.modules {
		for _, k := range e.Info.Roles {
			if k == role {
				entries = append(entries, e)
				break
			}
		}
	}
	sortEntries(entries)
	return entries
}

// ListByCapability returns the modules that declare capability, in a stable
// order: registration order (earliest first), then module ID. The first
// entry is the provider of record (see Provider).
func (r *Registry) ListByCapability(capability string) []*Entry {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if mods, ok := r.capIndex[capability]; ok {
		entries := make([]*Entry, 0, len(mods))
		for id := range mods {
			if e, exists := r.modules[id]; exists {
				entries = append(entries, e)
			}
		}
		sortEntries(entries)
		return entries
	}
	return nil
}

// Provider returns the provider of record for capability: the earliest
// registered module that still declares it (a same-ID replace keeps its
// place). Core wires exclusive capabilities (call/publish policy, auth,
// authorizer, identity) to this provider, never to an arbitrary one
// (ADR-0018, NFR-SEC-002).
func (r *Registry) Provider(capability string) (*Entry, bool) {
	entries := r.ListByCapability(capability)
	if len(entries) == 0 {
		return nil, false
	}
	return entries[0], true
}

func (r *Registry) Discover(ctx context.Context, role string) []contracts.ModuleInfo {
	entries := r.ListByRole(role)
	infos := make([]contracts.ModuleInfo, len(entries))
	for i, e := range entries {
		infos[i] = e.Info
	}
	return infos
}

// validTransitions defines the allowed state machine transitions.
// A nil value means the state is terminal (no transitions allowed).
var validTransitions = map[contracts.ModuleState][]contracts.ModuleState{
	contracts.ModuleStateRegistered: {contracts.ModuleStateStarting, contracts.ModuleStateDegraded},
	contracts.ModuleStateStarting:   {contracts.ModuleStateRunning, contracts.ModuleStateDegraded, contracts.ModuleStateStopping},
	contracts.ModuleStateRunning:    {contracts.ModuleStateDegraded, contracts.ModuleStateStopping},
	contracts.ModuleStateDegraded:   {contracts.ModuleStateRunning, contracts.ModuleStateStopping},
	contracts.ModuleStateStopping:   {contracts.ModuleStateStopped},
	contracts.ModuleStateStopped:    {}, // terminal — no transitions
}

func (r *Registry) SetState(id string, state contracts.ModuleState) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	entry, ok := r.modules[id]
	if !ok {
		return fmt.Errorf("module %q not found", id)
	}

	// Allow any transition from an empty/zero state (initial registration)
	if entry.State != "" {
		allowed, ok := validTransitions[entry.State]
		if !ok {
			return fmt.Errorf("invalid transition from %q to %q: source state unknown", entry.State, state)
		}
		valid := false
		for _, s := range allowed {
			if s == state {
				valid = true
				break
			}
		}
		if !valid {
			return fmt.Errorf("invalid transition from %q to %q", entry.State, state)
		}
	}

	entry.State = state
	slog.Debug("registry: module state changed", "id", id, "state", string(state))
	return nil
}

// Health returns the health error for the given module ID.
// Returns nil if the module is healthy, wraps contracts.ErrNotFound
// if the module is not registered, or returns the module's health error.
func (r *Registry) Health(id string) error {
	r.mu.RLock()
	defer r.mu.RUnlock()

	entry, ok := r.modules[id]
	if !ok {
		return fmt.Errorf("module %q not found: %w", id, contracts.ErrNotFound)
	}
	return entry.Health
}

func (r *Registry) SetHealth(id string, err error) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	entry, ok := r.modules[id]
	if !ok {
		return fmt.Errorf("module %q not found", id)
	}
	entry.Health = err
	if err != nil {
		slog.Warn("registry: module health degraded", "id", id, "error", err)
	} else {
		slog.Debug("registry: module health restored", "id", id)
	}
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

func (r *Registry) resolveDeps(entry *Entry, resolved, visiting map[string]bool) error {
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

// StartupOrder returns all module IDs in dependency-respecting order.
// Modules in DependsOn appear before the module that depends on them.
// Returns an error if a dependency cycle is detected.
func (r *Registry) StartupOrder() ([]string, error) {
	// Visit in registration order so the result is deterministic.
	listed := r.List()
	ids := make([]string, 0, len(listed))
	for _, e := range listed {
		ids = append(ids, e.Info.ID)
	}

	// Build adjacency: for each module, resolve its deps
	resolved := make(map[string]bool)
	ordered := make([]string, 0, len(ids))

	var visit func(id string, visiting map[string]bool) error
	visit = func(id string, visiting map[string]bool) error {
		if resolved[id] {
			return nil
		}
		if visiting[id] {
			return fmt.Errorf("circular dependency detected: %q", id)
		}
		visiting[id] = true

		r.mu.RLock()
		entry, ok := r.modules[id]
		r.mu.RUnlock()
		if !ok {
			visiting[id] = false
			return fmt.Errorf("module %q not found during ordering", id)
		}

		for _, depID := range entry.Deps {
			if err := visit(depID, visiting); err != nil {
				return err
			}
		}

		visiting[id] = false
		resolved[id] = true
		ordered = append(ordered, id)
		return nil
	}

	for _, id := range ids {
		if err := visit(id, make(map[string]bool)); err != nil {
			return nil, err
		}
	}

	return ordered, nil
}

// DependencyGraph returns all module IDs that list the given module
// in their DependsOn. Useful for understanding the impact of stopping
// or removing a module.
func (r *Registry) DependencyGraph(id string) ([]string, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if _, ok := r.modules[id]; !ok {
		return nil, fmt.Errorf("module %q not found", id)
	}

	var dependents []string
	for _, entry := range r.modules {
		for _, depID := range entry.Deps {
			if depID == id {
				dependents = append(dependents, entry.Info.ID)
				break
			}
		}
	}
	return dependents, nil
}

func (r *Registry) Count() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.modules)
}

func (r *Registry) FindByRole(role string) []contracts.ModuleEntry {
	entries := r.ListByRole(role)
	result := make([]contracts.ModuleEntry, len(entries))
	for i, e := range entries {
		result[i] = contracts.ModuleEntry{Info: e.Info, State: e.State, Module: e.Module}
	}
	return result
}

func (r *Registry) FindByCapability(capability string) []contracts.ModuleEntry {
	entries := r.ListByCapability(capability)
	result := make([]contracts.ModuleEntry, len(entries))
	for i, e := range entries {
		result[i] = contracts.ModuleEntry{Info: e.Info, State: e.State, Module: e.Module}
	}
	return result
}

func (r *Registry) SupportsCapability(moduleID, capability string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if mods, ok := r.capIndex[capability]; ok {
		return mods[moduleID]
	}
	return false
}

func (r *Registry) Resolve(id string) (contracts.ModuleEntry, error) {
	entry, err := r.Get(id)
	if err != nil {
		return contracts.ModuleEntry{}, err
	}
	return contracts.ModuleEntry{Info: entry.Info, State: entry.State, Module: entry.Module}, nil
}

func (r *Registry) ListAll() []contracts.ModuleEntry {
	entries := r.List()
	result := make([]contracts.ModuleEntry, len(entries))
	for i, e := range entries {
		result[i] = contracts.ModuleEntry{Info: e.Info, State: e.State, Module: e.Module}
	}
	return result
}
