package module

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/Muxcore-Media/core/pkg/contracts"

	"github.com/Muxcore-Media/core/internal/registry"
	"github.com/google/uuid"
)

type Manager struct {
	registry *registry.Registry
	bus      contracts.EventBus
	audit    contracts.AuditLogger
}

func NewManager(reg *registry.Registry, bus contracts.EventBus) *Manager {
	return &Manager{registry: reg, bus: bus}
}

// SetAuditLogger attaches an audit logger for recording module lifecycle events.
func (m *Manager) SetAuditLogger(a contracts.AuditLogger) {
	m.audit = a
}

func (m *Manager) Register(mod contracts.Module, deps []string) error {
	info := mod.Info()
	if err := m.registry.Register(mod, deps); err != nil {
		return err
	}

	m.auditLifecycle("module.register", info.ID, map[string]string{"version": info.Version})
	m.publishModuleRegistered(info)
	return nil
}

func (m *Manager) Unregister(id string) error {
	entry, err := m.registry.Get(id)
	if err != nil {
		return err
	}

	if err := m.registry.Unregister(id); err != nil {
		return err
	}

	m.auditLifecycle("module.unregister", id, nil)
	m.publishModuleUnregistered(entry.Info)
	return nil
}

func (m *Manager) InitAll(ctx context.Context) error {
	entries := m.registry.List()

	order, err := m.startupOrder(entries)
	if err != nil {
		return fmt.Errorf("resolving startup order: %w", err)
	}

	for _, entry := range order {
		if _, err := m.registry.ResolveDeps(entry.Info.ID); err != nil {
			slog.Warn("unresolved dependencies, skipping module", "id", entry.Info.ID, "error", err)
			m.registry.SetState(entry.Info.ID, contracts.ModuleStateDegraded)
			m.publishModuleDegraded(entry.Info, err)
			continue
		}
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
		if _, err := m.registry.ResolveDeps(entry.Info.ID); err != nil {
			slog.Warn("unresolved dependencies, skipping module", "id", entry.Info.ID, "error", err)
			m.registry.SetState(entry.Info.ID, contracts.ModuleStateDegraded)
			m.publishModuleDegraded(entry.Info, err)
			continue
		}
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

	var errs []error
	for _, entry := range order {
		slog.Info("stopping module", "id", entry.Info.ID)
		m.registry.SetState(entry.Info.ID, contracts.ModuleStateStopping)
		if err := entry.Module.Stop(ctx); err != nil {
			slog.Error("error stopping module", "id", entry.Info.ID, "error", err)
			errs = append(errs, fmt.Errorf("stop %q: %w", entry.Info.ID, err))
		}
		m.registry.SetState(entry.Info.ID, contracts.ModuleStateStopped)
		m.auditLifecycle("module.stop", entry.Info.ID, nil)
	}
	return errors.Join(errs...)
}

func (m *Manager) HealthCheck(ctx context.Context) map[string]error {
	results := make(map[string]error)
	for _, entry := range m.registry.List() {
		err := entry.Module.Health(ctx)
		m.registry.SetHealth(entry.Info.ID, err)
		results[entry.Info.ID] = err
		if err != nil {
			m.auditLifecycle("module.degraded", entry.Info.ID, map[string]string{"error": err.Error()})
			m.publishModuleDegraded(entry.Info, err)
		}
	}
	return results
}

func (m *Manager) initOne(ctx context.Context, entry *registry.Entry) error {
	m.registry.SetState(entry.Info.ID, contracts.ModuleStateStarting)
	if err := entry.Module.Init(ctx); err != nil {
		return err
	}
	m.auditLifecycle("module.init", entry.Info.ID, map[string]string{"version": entry.Info.Version})
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
	m.auditLifecycle("module.start", entry.Info.ID, nil)
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

	return order, nil
}

// auditLifecycle records a module lifecycle event via the audit logger.
// It is a fire-and-forget operation; failures are silently dropped.
func (m *Manager) auditLifecycle(action, moduleID string, details map[string]string) {
	if m.audit == nil {
		return
	}
	if details == nil {
		details = make(map[string]string)
	}
	details["module_id"] = moduleID
	go func() {
		entry := contracts.AuditEntry{
			ID:        uuid.New().String(),
			Timestamp: time.Now(),
			Actor:     "system",
			Action:    action,
			Resource:  moduleID,
			Details:   details,
		}
		if err := m.audit.Log(context.Background(), entry); err != nil {
			slog.Error("audit log write failed", "action", action, "error", err)
		}
	}()
}

// publishModuleRegistered publishes a module.registered event on the event bus.
// If no bus is configured, the event is silently dropped.
func (m *Manager) publishModuleRegistered(info contracts.ModuleInfo) {
	if m.bus == nil {
		return
	}
	payload, err := json.Marshal(contracts.ModuleRegisteredPayload{
		ModuleID: info.ID,
		Version:  info.Version,
	})
	if err != nil {
		slog.Error("failed to marshal module.registered event", "module", info.ID, "error", err)
		return
	}
	_ = m.bus.Publish(context.Background(), contracts.Event{
		Type:    contracts.EventModuleRegistered,
		Source:  info.ID,
		Payload: payload,
	})
}

// publishModuleUnregistered publishes a module.unregistered event on the event bus.
func (m *Manager) publishModuleUnregistered(info contracts.ModuleInfo) {
	if m.bus == nil {
		return
	}
	payload, err := json.Marshal(contracts.ModuleUnregisteredPayload{
		ModuleID: info.ID,
	})
	if err != nil {
		slog.Error("failed to marshal module.unregistered event", "module", info.ID, "error", err)
		return
	}
	_ = m.bus.Publish(context.Background(), contracts.Event{
		Type:    contracts.EventModuleUnregistered,
		Source:  info.ID,
		Payload: payload,
	})
}

// publishModuleDegraded publishes a module.degraded event on the event bus.
func (m *Manager) publishModuleDegraded(info contracts.ModuleInfo, err error) {
	if m.bus == nil {
		return
	}
	payload, marshalErr := json.Marshal(contracts.ModuleDegradedPayload{
		ModuleID: info.ID,
		Error:    err.Error(),
	})
	if marshalErr != nil {
		slog.Error("failed to marshal module.degraded event", "module", info.ID, "error", marshalErr)
		return
	}
	_ = m.bus.Publish(context.Background(), contracts.Event{
		Type:    contracts.EventModuleDegraded,
		Source:  info.ID,
		Payload: payload,
	})
}
