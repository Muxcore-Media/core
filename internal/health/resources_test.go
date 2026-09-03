package health

import (
	"os"
	"testing"
)

func TestRuntimeStats(t *testing.T) {
	stats := RuntimeStats()
	if stats.MemoryBytes == 0 {
		t.Error("expected non-zero memory allocation")
	}
	if stats.Goroutines <= 0 {
		t.Error("expected positive goroutine count")
	}
}

func TestProcMemoryBytes_CurrentProcess(t *testing.T) {
	mem, ok := ProcMemoryBytes(os.Getpid())
	if !ok {
		t.Skip("process memory stats unavailable on this platform")
	}
	if mem == 0 {
		t.Error("expected non-zero process memory")
	}
}

func TestProcStats_CurrentProcess(t *testing.T) {
	stats, ok := ProcStats(os.Getpid())
	if !ok {
		t.Skip("process stats unavailable on this platform")
	}
	if stats.MemoryBytes == 0 {
		t.Error("expected non-zero memory_bytes")
	}
}
