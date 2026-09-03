package mgr

import (
	"testing"
	"time"
)

func TestRestartConfigBackoff(t *testing.T) {
	cfg := RestartConfig{MaxRetries: 3, InitialBackoff: time.Second, MaxBackoff: 8 * time.Second}
	if got := cfg.BackoffDuration(0); got != time.Second {
		t.Fatalf("attempt 0 backoff = %s", got)
	}
	if got := cfg.BackoffDuration(2); got != 4*time.Second {
		t.Fatalf("attempt 2 backoff = %s", got)
	}
	if got := cfg.BackoffDuration(10); got != 8*time.Second {
		t.Fatalf("attempt 10 backoff = %s", got)
	}
}

func TestRestartConfigShouldRestart(t *testing.T) {
	cfg := RestartConfig{MaxRetries: 2}
	if !cfg.ShouldRestart(RestartOnFailure, false, 0) {
		t.Fatal("expected restart on failure")
	}
	if cfg.ShouldRestart(RestartOnFailure, true, 0) {
		t.Fatal("expected no restart on clean exit")
	}
	if cfg.ShouldRestart(RestartOnFailure, false, 2) {
		t.Fatal("expected max retries enforced")
	}
}

func TestParseRestartConfig(t *testing.T) {
	policy, cfg := ParseRestartConfig(RestartNever, map[string]string{
		"restart_policy":          "on-failure",
		"restart_max_retries":     "3",
		"restart_initial_backoff": "2s",
		"restart_max_backoff":     "1m",
	})
	if policy != RestartOnFailure {
		t.Fatalf("policy = %q", policy)
	}
	if cfg.MaxRetries != 3 || cfg.InitialBackoff != 2*time.Second || cfg.MaxBackoff != time.Minute {
		t.Fatalf("cfg = %+v", cfg)
	}
}
