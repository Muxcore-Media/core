package circuitbreaker_test

import (
	"testing"
	"time"

	"github.com/Muxcore-Media/core/internal/circuitbreaker"
)

func fixedClock(t *time.Time) func() time.Time {
	return func() time.Time { return *t }
}

func advance(clock *time.Time, d time.Duration) {
	*clock = clock.Add(d)
}

func TestConfigFromEnv_Defaults(t *testing.T) {
	t.Setenv("CIRCUIT_BREAKER_PROBE_INTERVAL", "")
	t.Setenv("CIRCUIT_BREAKER_FAIL_THRESHOLD", "")
	t.Setenv("CIRCUIT_BREAKER_RECOVERY_THRESHOLD", "")

	cfg := circuitbreaker.ConfigFromEnv()
	if cfg.ProbeInterval != 30*time.Second {
		t.Fatalf("probe interval = %v, want 30s", cfg.ProbeInterval)
	}
	if cfg.FailThreshold != 3 {
		t.Fatalf("fail threshold = %d, want 3", cfg.FailThreshold)
	}
	if cfg.RecoveryThreshold != 1 {
		t.Fatalf("recovery threshold = %d, want 1", cfg.RecoveryThreshold)
	}
}

func TestConfigFromEnv_Overrides(t *testing.T) {
	t.Setenv("CIRCUIT_BREAKER_PROBE_INTERVAL", "5s")
	t.Setenv("CIRCUIT_BREAKER_FAIL_THRESHOLD", "2")
	t.Setenv("CIRCUIT_BREAKER_RECOVERY_THRESHOLD", "2")

	cfg := circuitbreaker.ConfigFromEnv()
	if cfg.ProbeInterval != 5*time.Second {
		t.Fatalf("probe interval = %v, want 5s", cfg.ProbeInterval)
	}
	if cfg.FailThreshold != 2 {
		t.Fatalf("fail threshold = %d, want 2", cfg.FailThreshold)
	}
	if cfg.RecoveryThreshold != 2 {
		t.Fatalf("recovery threshold = %d, want 2", cfg.RecoveryThreshold)
	}
}

func TestBreaker_ClosedStaysClosedBelowThreshold(t *testing.T) {
	reg := circuitbreaker.NewRegistry(circuitbreaker.Config{
		FailThreshold:     3,
		RecoveryThreshold: 1,
		ProbeInterval:     time.Second,
	})

	reg.RecordHealth("mod-a", false)
	reg.RecordHealth("mod-a", false)

	st := reg.Snapshot("mod-a")
	if st.State != circuitbreaker.StateClosed {
		t.Fatalf("state = %q, want CLOSED", st.State)
	}
	if st.FailureCount != 2 {
		t.Fatalf("failures = %d, want 2", st.FailureCount)
	}
}

func TestBreaker_ClosedToOpenAtFailThreshold(t *testing.T) {
	reg := circuitbreaker.NewRegistry(circuitbreaker.Config{
		FailThreshold:     3,
		RecoveryThreshold: 1,
		ProbeInterval:     time.Second,
	})

	for i := 0; i < 3; i++ {
		reg.RecordHealth("mod-a", false)
	}

	st := reg.Snapshot("mod-a")
	if st.State != circuitbreaker.StateOpen {
		t.Fatalf("state = %q, want OPEN", st.State)
	}
	if st.OpenedAt.IsZero() {
		t.Fatal("opened_at should be set")
	}
}

func TestBreaker_TripOpensImmediately(t *testing.T) {
	reg := circuitbreaker.NewRegistry(circuitbreaker.Config{
		FailThreshold:     3,
		RecoveryThreshold: 1,
		ProbeInterval:     time.Second,
	})

	reg.Trip("degraded-mod")

	st := reg.Snapshot("degraded-mod")
	if st.State != circuitbreaker.StateOpen {
		t.Fatalf("state = %q, want OPEN", st.State)
	}
}

func TestBreaker_OpenToHalfOpenAfterProbeInterval(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	reg := circuitbreaker.NewRegistry(circuitbreaker.Config{
		FailThreshold:     1,
		RecoveryThreshold: 1,
		ProbeInterval:     10 * time.Second,
	})
	reg.SetClock(fixedClock(&now))

	reg.Trip("mod-a")
	st := reg.Snapshot("mod-a")
	if st.State != circuitbreaker.StateOpen {
		t.Fatalf("state = %q, want OPEN", st.State)
	}

	advance(&now, 9*time.Second)
	reg.RecordHealth("mod-a", false)
	st = reg.Snapshot("mod-a")
	if st.State != circuitbreaker.StateOpen {
		t.Fatalf("before probe interval: state = %q, want OPEN", st.State)
	}

	advance(&now, 1*time.Second)
	reg.RecordHealth("mod-a", true)
	st = reg.Snapshot("mod-a")
	if st.State != circuitbreaker.StateHalfOpen && st.State != circuitbreaker.StateClosed {
		// After probe, success in half-open with recovery threshold 1 closes.
	}
	if st.State != circuitbreaker.StateClosed {
		t.Fatalf("after probe + success: state = %q, want CLOSED", st.State)
	}
}

func TestBreaker_HalfOpenFailureReturnsToOpen(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	reg := circuitbreaker.NewRegistry(circuitbreaker.Config{
		FailThreshold:     1,
		RecoveryThreshold: 1,
		ProbeInterval:     5 * time.Second,
	})
	reg.SetClock(fixedClock(&now))

	reg.Trip("mod-a")
	advance(&now, 5*time.Second)
	reg.RecordHealth("mod-a", false) // advances to half-open then fails

	st := reg.Snapshot("mod-a")
	if st.State != circuitbreaker.StateOpen {
		t.Fatalf("state = %q, want OPEN after half-open failure", st.State)
	}
}

func TestBreaker_HalfOpenSuccessRestoresClosed(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	reg := circuitbreaker.NewRegistry(circuitbreaker.Config{
		FailThreshold:     1,
		RecoveryThreshold: 1,
		ProbeInterval:     5 * time.Second,
	})
	reg.SetClock(fixedClock(&now))

	reg.Trip("mod-a")
	advance(&now, 5*time.Second)
	reg.RecordHealth("mod-a", true)

	st := reg.Snapshot("mod-a")
	if st.State != circuitbreaker.StateClosed {
		t.Fatalf("state = %q, want CLOSED", st.State)
	}
	if st.FailureCount != 0 {
		t.Fatalf("failures = %d, want 0 after recovery", st.FailureCount)
	}
}

func TestBreaker_RecoveryThresholdRequiresMultipleSuccesses(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	reg := circuitbreaker.NewRegistry(circuitbreaker.Config{
		FailThreshold:     1,
		RecoveryThreshold: 2,
		ProbeInterval:     5 * time.Second,
	})
	reg.SetClock(fixedClock(&now))

	reg.Trip("mod-a")
	advance(&now, 5*time.Second)
	reg.RecordHealth("mod-a", true)

	st := reg.Snapshot("mod-a")
	if st.State != circuitbreaker.StateHalfOpen {
		t.Fatalf("after one success: state = %q, want HALF_OPEN", st.State)
	}

	reg.RecordHealth("mod-a", true)
	st = reg.Snapshot("mod-a")
	if st.State != circuitbreaker.StateClosed {
		t.Fatalf("after two successes: state = %q, want CLOSED", st.State)
	}
}

func TestBreaker_ClosedSuccessResetsFailureCount(t *testing.T) {
	reg := circuitbreaker.NewRegistry(circuitbreaker.Config{
		FailThreshold:     3,
		RecoveryThreshold: 1,
		ProbeInterval:     time.Second,
	})

	reg.RecordHealth("mod-a", false)
	reg.RecordHealth("mod-a", false)
	reg.RecordHealth("mod-a", true)

	st := reg.Snapshot("mod-a")
	if st.State != circuitbreaker.StateClosed {
		t.Fatalf("state = %q, want CLOSED", st.State)
	}
	if st.FailureCount != 0 {
		t.Fatalf("failures = %d, want 0 after success", st.FailureCount)
	}
}

func TestRegistry_SnapshotAll(t *testing.T) {
	reg := circuitbreaker.NewRegistry(circuitbreaker.Config{FailThreshold: 1})
	reg.Trip("a")
	reg.Trip("b")

	all := reg.SnapshotAll()
	if len(all) != 2 {
		t.Fatalf("snapshot count = %d, want 2", len(all))
	}
	if all["a"].State != circuitbreaker.StateOpen {
		t.Fatalf("a state = %q, want OPEN", all["a"].State)
	}
}

func TestRegistry_SnapshotUnknownModule(t *testing.T) {
	reg := circuitbreaker.NewRegistry(circuitbreaker.Config{})
	st := reg.Snapshot("unknown")
	if st.State != circuitbreaker.StateClosed {
		t.Fatalf("state = %q, want CLOSED for unknown module", st.State)
	}
}

func TestBreaker_FullLifecycle(t *testing.T) {
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	reg := circuitbreaker.NewRegistry(circuitbreaker.Config{
		FailThreshold:     2,
		RecoveryThreshold: 1,
		ProbeInterval:     30 * time.Second,
	})
	reg.SetClock(fixedClock(&now))

	// CLOSED → accumulate failures
	reg.RecordHealth("svc", false)
	reg.RecordHealth("svc", false)
	if st := reg.Snapshot("svc"); st.State != circuitbreaker.StateOpen {
		t.Fatalf("step 1: state = %q, want OPEN", st.State)
	}

	// OPEN → wait probe interval → HALF_OPEN on next check
	advance(&now, 30*time.Second)
	reg.RecordHealth("svc", false)
	if st := reg.Snapshot("svc"); st.State != circuitbreaker.StateOpen {
		t.Fatalf("step 2: state = %q, want OPEN after failed probe", st.State)
	}

	advance(&now, 30*time.Second)
	reg.RecordHealth("svc", true)
	if st := reg.Snapshot("svc"); st.State != circuitbreaker.StateClosed {
		t.Fatalf("step 3: state = %q, want CLOSED after successful probe", st.State)
	}
}
