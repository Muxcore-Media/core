package health

import (
	"bufio"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
)

// ModuleResourceStats holds optional per-module runtime metrics.
// Goroutines is omitted from JSON when unavailable (negative value).
type ModuleResourceStats struct {
	MemoryBytes uint64 `json:"memory_bytes,omitempty"`
	Goroutines  int    `json:"goroutines,omitempty"`
}

// ProcMemoryBytes reads resident set size for a process PID.
// Returns ok=false when the platform does not expose process memory or the PID is gone.
func ProcMemoryBytes(pid int) (uint64, bool) {
	if pid <= 0 {
		return 0, false
	}
	f, err := os.Open(fmt.Sprintf("/proc/%d/status", pid)) //nolint:gosec // pid is validated
	if err != nil {
		return 0, false
	}
	defer func() { _ = f.Close() }()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "VmRSS:") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			return 0, false
		}
		kb, err := strconv.ParseUint(fields[1], 10, 64)
		if err != nil {
			return 0, false
		}
		return kb * 1024, true
	}
	return 0, false
}

// ProcStats collects memory for an OS process. Goroutines are not available for
// external processes and are left at zero (omitted from JSON).
func ProcStats(pid int) (ModuleResourceStats, bool) {
	mem, ok := ProcMemoryBytes(pid)
	if !ok {
		return ModuleResourceStats{}, false
	}
	return ModuleResourceStats{MemoryBytes: mem}, true
}

// RuntimeStats returns memory and goroutine counts for the current process.
func RuntimeStats() ModuleResourceStats {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return ModuleResourceStats{
		MemoryBytes: m.Alloc,
		Goroutines:  runtime.NumGoroutine(),
	}
}

// ResourceStatsProvider is optionally implemented by in-process modules that
// can report their own resource usage.
type ResourceStatsProvider interface {
	ResourceStats() ModuleResourceStats
}
