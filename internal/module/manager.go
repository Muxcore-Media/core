package module

import (
	"context"
	"fmt"
	"log/slog"
	"sort"

	"github.com/Muxcore-Media/core/pkg/contracts"

	"github.com/Muxcore-Media/core/internal/registry"
)

type Manager struct {
	registry *registry.Registry
}

func NewManager(reg *registry.Registry) *Manager {
	return &Manager{registry: reg}
}

func (m *Manager) Register(mod contracts.Module, deps []string) error {
	return m.registry.Register(mod, deps)
}

func (m *Manager) InitAll(ctx context.Context) error {
	entries := m.registry.List()

	order, err := m.startupOrder(entries)
	if err != nil {
		return fmt.Errorf("resolving startup order: %w", err)
	}

	for _, entry := range order {
		slog.Info("initializing module", "id", entry.Info.ID, "version", entry.Info.Version)
		if err := m.initOne(ctx, entry); err != nil {
			return fmt.Errorf("init %q: %w", entry.Info.ID, err)
		}
	}
	return nil
}

func (m *Manager) StartAll(ctx context.Context) error {
	entries := m.registry.List()

	order, err := m.startupOrder(entries)
	if err != nil {
		return fmt.Errorf("resolving startup order: %w", err)
	}

	for _, entry := range order {
		slog.Info("starting module", "id", entry.Info.ID)
		if err := m.startOne(ctx, entry); err != nil {
			return fmt.Errorf("start %q: %w", entry.Info.ID, err)
		}
	}
	return nil
}

func (m *Manager) StopAll(ctx context.Context) error {
	entries := m.registry.List()

	// Shutdown in reverse order
	order, err := m.startupOrder(entries)
	if err != nil {
		return fmt.Errorf("resolving shutdown order: %w", err)
	}
	for i, j := 0, len(order)-1; i < j; i, j = i+1, j-1 {
		order[i], order[j] = order[j], order[i]
	}

	for _, entry := range order {
		slog.Info("stopping module", "id", entry.Info.ID)
		m.registry.SetState(entry.Info.ID, contracts.ModuleStateStopping)
		if err := entry.Module.Stop(ctx); err != nil {
			slog.Error("error stopping module", "id", entry.Info.ID, "error", err)
		}
		m.registry.SetState(entry.Info.ID, contracts.ModuleStateStopped)
	}
	return nil
}

func (m *Manager) HealthCheck(ctx context.Context) map[string]error {
	results := make(map[string]error)
	for _, entry := range m.registry.List() {
		err := entry.Module.Health(ctx)
		m.registry.SetHealth(entry.Info.ID, err)
		results[entry.Info.ID] = err
	}
	return results
}

func (m *Manager) initOne(ctx context.Context, entry *registry.Entry) error {
	m.registry.SetState(entry.Info.ID, contracts.ModuleStateStarting)
	if err := entry.Module.Init(ctx); err != nil {
		return err
	}
	return nil
}

func (m *Manager) startOne(ctx context.Context, entry *registry.Entry) error {
	if entry.State == contracts.ModuleStateRunning {
		return nil
	}
	m.registry.SetState(entry.Info.ID, contracts.ModuleStateStarting)
	if err := entry.Module.Start(ctx); err != nil {
		return err
	}
	m.registry.SetState(entry.Info.ID, contracts.ModuleStateRunning)
	return nil
}

func (m *Manager) startupOrder(entries []*registry.Entry) ([]*registry.Entry, error) {
	byID := make(map[string]*registry.Entry, len(entries))
	for _, e := range entries {
		byID[e.Info.ID] = e
	}

	visited := make(map[string]bool)
	perm := make(map[string]bool)
	var order []*registry.Entry

	var visit func(id string) error
	visit = func(id string) error {
		if perm[id] {
			return nil
		}
		if visited[id] {
			return fmt.Errorf("circular dependency: %q", id)
		}
		visited[id] = true

		entry, ok := byID[id]
		if !ok {
			return fmt.Errorf("module %q not in registry", id)
		}
		for _, dep := range entry.Deps {
			if err := visit(dep); err != nil {
				return err
			}
		}

		perm[id] = true
		order = append(order, entry)
		return nil
	}

	for _, e := range entries {
		if !perm[e.Info.ID] {
			if err := visit(e.Info.ID); err != nil {
				return nil, err
			}
		}
	}

	// Sort leaves (no deps) first, then by dependency count for deterministic ordering
	sort.SliceStable(order, func(i, j int) bool {
		return len(order[i].Deps) < len(order[j].Deps)
	})

	return order, nil
}
