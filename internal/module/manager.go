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
	"github.com/Muxcore-Media/core/internal/version"
	"github.com/google/uuid"
)

var ErrProcessExited = errors.New("process exited")

// auditSem limits concurrent audit goroutines to prevent unbounded bursts.
var auditSem = make(chan struct{}, 100)

// Restarter is implemented by the sidecar manager to support health-triggered
// module restarts. The lifecycle Manager uses this to restart unhealthy
// modules that were spawned as sidecar processes.
type Restarter interface {
	RestartModule(ctx context.Context, moduleID string) error
}

type Manager struct {
	registry  *registry.Registry
	bus       contracts.EventBus
	audit     contracts.AuditLogger
	restarter Restarter
}

func NewManager(reg *registry.Registry, bus contracts.EventBus) *Manager {
	return &Manager{registry: reg, bus: bus}
}

// SetRestarter attaches a Restarter for health-triggered module restarts.
func (m *Manager) SetRestarter(r Restarter) {
	m.restarter = r
}

// SetAuditLogger attaches an audit logger for recording module lifecycle events.
func (m *Manager) SetAuditLogger(a contracts.AuditLogger) {
	m.audit = a
}

func (m *Manager) Register(ctx context.Context, mod contracts.Module, deps []string) error {
	_, err := m.RegisterWith(ctx, mod, deps, registry.RegisterOptions{})
	return err
}

// RegisterWith is Register with registry options (atomic replace of the same
// ID, exclusive capabilities). It reports whether an existing entry was
// replaced.
func (m *Manager) RegisterWith(ctx context.Context, mod contracts.Module, deps []string, opts registry.RegisterOptions) (bool, error) {
	info := mod.Info()

	// Check core version compatibility before registration.
	if err := version.CheckModule(info.ID, info.MinCoreVersion); err != nil {
		slog.Error("module version incompatible with core",
			"module", info.ID,
			"min_core_version", info.MinCoreVersion,
			"core_version", version.String(),
			"error", err,
		)
		return false, err
	}

	replaced, err := m.registry.RegisterWith(mod, deps, opts)
	if err != nil {
		return false, err
	}

	details := map[string]string{"version": info.Version}
	if replaced {
		details["replaced"] = "true"
	}
	m.auditLifecycle(ctx, "module.register", info.ID, details)
	m.publishModuleRegistered(ctx, info)
	return replaced, nil
}

func (m *Manager) Unregister(ctx context.Context, id string) error {
	entry, err := m.registry.Get(id)
	if err != nil {
		return err
	}

	if err := m.registry.Unregister(id); err != nil {
		return err
	}

	m.auditLifecycle(ctx, "module.unregister", id, nil)
	m.publishModuleUnregistered(ctx, entry.Info)
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
			if setErr := m.registry.SetState(entry.Info.ID, contracts.ModuleStateDegraded); setErr != nil {
				slog.Error("failed to set module state", "id", entry.Info.ID, "state", contracts.ModuleStateDegraded, "error", setErr)
			}
			m.publishModuleDegraded(ctx, entry.Info, err)
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
			if setErr := m.registry.SetState(entry.Info.ID, contracts.ModuleStateDegraded); setErr != nil {
				slog.Error("failed to set module state", "id", entry.Info.ID, "state", contracts.ModuleStateDegraded, "error", setErr)
			}
			m.publishModuleDegraded(ctx, entry.Info, err)
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
		if setErr := m.registry.SetState(entry.Info.ID, contracts.ModuleStateStopping); setErr != nil {
			slog.Error("failed to set module state", "id", entry.Info.ID, "state", contracts.ModuleStateStopping, "error", setErr)
		}
		if err := entry.Module.Stop(ctx); err != nil {
			slog.Error("error stopping module", "id", entry.Info.ID, "error", err)
			errs = append(errs, fmt.Errorf("stop %q: %w", entry.Info.ID, err))
		}
		if setErr := m.registry.SetState(entry.Info.ID, contracts.ModuleStateStopped); setErr != nil {
			slog.Error("failed to set module state", "id", entry.Info.ID, "state", contracts.ModuleStateStopped, "error", setErr)
		}
		m.auditLifecycle(ctx, "module.stop", entry.Info.ID, nil)
	}
	return errors.Join(errs...)
}

// DefaultHealthCheckInterval is the default interval between health checks.
const DefaultHealthCheckInterval = 30 * time.Second

// StartHealthCheckLoop periodically checks module health and triggers
// remediation (restart) for unhealthy sidecar modules.
func (m *Manager) StartHealthCheckLoop(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = DefaultHealthCheckInterval
	}
	go func() {
		defer func() {
			if r := recover(); r != nil {
				slog.Error("health check loop panic recovered", "panic", r)
			}
		}()
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		slog.Info("health check loop started", "interval", interval)
		for {
			select {
			case <-ctx.Done():
				slog.Info("health check loop stopped")
				return
			case <-ticker.C:
				m.healthCheckAndRemediate(ctx)
			}
		}
	}()
}

func (m *Manager) healthCheckAndRemediate(ctx context.Context) {
	unhealthy := m.HealthCheck(ctx)
	for moduleID, err := range unhealthy {
		if err == nil {
			continue
		}
		slog.Warn("module unhealthy", "module", moduleID, "error", err)
		if m.restarter == nil {
			continue
		}
		if err := m.restarter.RestartModule(ctx, moduleID); err != nil {
			slog.Error("failed to restart unhealthy module",
				"module", moduleID, "error", err)
		} else {
			slog.Info("module restarted after health failure",
				"module", moduleID)
		}
	}
}

func (m *Manager) HealthCheck(ctx context.Context) map[string]error {
	results := make(map[string]error)
	for _, entry := range m.registry.List() {
		err := entry.Module.Health(ctx)
		if setErr := m.registry.SetHealth(entry.Info.ID, err); setErr != nil {
			slog.Error("failed to set module health", "id", entry.Info.ID, "error", setErr)
		}
		results[entry.Info.ID] = err
		if err != nil {
			m.auditLifecycle(ctx, "module.degraded", entry.Info.ID, map[string]string{"error": err.Error()})
			m.publishModuleDegraded(ctx, entry.Info, err)
		}
	}
	return results
}

func (m *Manager) initOne(ctx context.Context, entry *registry.Entry) error {
	if entry.State != contracts.ModuleStateRegistered {
		return nil // already initialized or beyond
	}
	if setErr := m.registry.SetState(entry.Info.ID, contracts.ModuleStateStarting); setErr != nil {
		return fmt.Errorf("set state %s for %q: %w", contracts.ModuleStateStarting, entry.Info.ID, setErr)
	}
	if err := entry.Module.Init(ctx); err != nil {
		return err
	}
	m.auditLifecycle(ctx, "module.init", entry.Info.ID, map[string]string{"version": entry.Info.Version})
	return nil
}

func (m *Manager) startOne(ctx context.Context, entry *registry.Entry) error {
	switch entry.State {
	case contracts.ModuleStateRunning:
		return nil
	case contracts.ModuleStateRegistered:
		if setErr := m.registry.SetState(entry.Info.ID, contracts.ModuleStateStarting); setErr != nil {
			return fmt.Errorf("set state %s for %q: %w", contracts.ModuleStateStarting, entry.Info.ID, setErr)
		}
	case contracts.ModuleStateStarting:
		// Already in starting state (from initOne); proceed to start.
	default:
		return fmt.Errorf("start: invalid state %q for %q", entry.State, entry.Info.ID)
	}
	if err := entry.Module.Start(ctx); err != nil {
		return err
	}
	if setErr := m.registry.SetState(entry.Info.ID, contracts.ModuleStateRunning); setErr != nil {
		slog.Error("failed to set module state", "id", entry.Info.ID, "state", contracts.ModuleStateRunning, "error", setErr)
	}
	m.auditLifecycle(ctx, "module.start", entry.Info.ID, nil)
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
func (m *Manager) auditLifecycle(ctx context.Context, action, moduleID string, details map[string]string) {
	if m.audit == nil {
		return
	}
	if details == nil {
		details = make(map[string]string)
	}
	details["module_id"] = moduleID

	select {
	case auditSem <- struct{}{}:
	default:
		slog.Warn("audit lifecycle: too many concurrent audits, dropping", "action", action)
		return
	}

	go func() {
		defer func() {
			<-auditSem
			if r := recover(); r != nil {
				slog.Error("audit lifecycle panic recovered", "action", action, "module", moduleID, "panic", r)
			}
		}()
		entry := contracts.AuditEntry{
			ID:        uuid.New().String(),
			Timestamp: time.Now(),
			Actor:     "system",
			Action:    action,
			Resource:  moduleID,
			Details:   details,
		}
		if err := m.audit.Log(ctx, entry); err != nil {
			slog.Error("audit log write failed", "action", action, "error", err)
		}
	}()
}

// publishModuleRegistered publishes a module.registered event on the event bus.
// If no bus is configured, the event is silently dropped.
func (m *Manager) publishModuleRegistered(ctx context.Context, info contracts.ModuleInfo) {
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
	if err := m.bus.Publish(ctx, contracts.Event{
		Type:    contracts.EventModuleRegistered,
		Source:  info.ID,
		Payload: payload,
	}); err != nil {
		slog.Warn("publish module.registered event failed", "module", info.ID, "error", err)
	}
}

// publishModuleUnregistered publishes a module.unregistered event on the event bus.
func (m *Manager) publishModuleUnregistered(ctx context.Context, info contracts.ModuleInfo) {
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
	if err := m.bus.Publish(ctx, contracts.Event{
		Type:    contracts.EventModuleUnregistered,
		Source:  info.ID,
		Payload: payload,
	}); err != nil {
		slog.Warn("publish module.unregistered event failed", "module", info.ID, "error", err)
	}
}

// publishModuleDegraded publishes a module.degraded event on the event bus.
func (m *Manager) publishModuleDegraded(ctx context.Context, info contracts.ModuleInfo, err error) {
	if m.bus == nil {
		return
	}
	errStr := ""
	if err != nil {
		errStr = err.Error()
	}
	payload, marshalErr := json.Marshal(contracts.ModuleDegradedPayload{
		ModuleID: info.ID,
		Error:    errStr,
	})
	if marshalErr != nil {
		slog.Error("failed to marshal module.degraded event", "module", info.ID, "error", marshalErr)
		return
	}
	if err := m.bus.Publish(ctx, contracts.Event{
		Type:    contracts.EventModuleDegraded,
		Source:  info.ID,
		Payload: payload,
	}); err != nil {
		slog.Warn("publish module.degraded event failed", "module", info.ID, "error", err)
	}
}
