package mgr

import (
	corehealth "github.com/Muxcore-Media/core/internal/health"
)

// ProcessResourceStats returns memory usage for tracked sidecar module processes.
// Goroutine counts are not available for external OS processes.
func (m *Manager) ProcessResourceStats() map[string]corehealth.ModuleResourceStats {
	m.mu.Lock()
	defer m.mu.Unlock()

	out := make(map[string]corehealth.ModuleResourceStats, len(m.processes))
	for id, cmd := range m.processes {
		if cmd == nil || cmd.Process == nil {
			continue
		}
		if cmd.ProcessState != nil && cmd.ProcessState.Exited() {
			continue
		}
		if stats, ok := corehealth.ProcStats(cmd.Process.Pid); ok {
			out[id] = stats
		}
	}
	return out
}

