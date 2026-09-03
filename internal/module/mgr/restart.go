package mgr

import (
	"strconv"
	"strings"
	"time"
)

// RestartConfig controls exponential backoff restart behavior for sidecar modules.
type RestartConfig struct {
	// MaxRetries is the maximum consecutive restart attempts (0 = use default).
	MaxRetries int
	// InitialBackoff is the delay before the first restart (0 = 1s).
	InitialBackoff time.Duration
	// MaxBackoff caps exponential backoff growth (0 = 30s).
	MaxBackoff time.Duration
}

// DefaultRestartConfig matches the legacy hardcoded restart behavior.
var DefaultRestartConfig = RestartConfig{
	MaxRetries:     5,
	InitialBackoff: time.Second,
	MaxBackoff:     30 * time.Second,
}

// Effective returns config with zero values replaced by defaults.
func (c RestartConfig) Effective() RestartConfig {
	out := c
	if out.MaxRetries <= 0 {
		out.MaxRetries = DefaultRestartConfig.MaxRetries
	}
	if out.InitialBackoff <= 0 {
		out.InitialBackoff = DefaultRestartConfig.InitialBackoff
	}
	if out.MaxBackoff <= 0 {
		out.MaxBackoff = DefaultRestartConfig.MaxBackoff
	}
	return out
}

// BackoffDuration returns the wait duration for the given attempt (0-based).
func (c RestartConfig) BackoffDuration(attempt int) time.Duration {
	cfg := c.Effective()
	if attempt < 0 {
		attempt = 0
	}
	backoff := cfg.InitialBackoff * (1 << min(attempt, 10))
	if backoff > cfg.MaxBackoff {
		backoff = cfg.MaxBackoff
	}
	return backoff
}

// ShouldRestart reports whether another restart is allowed for attempt (0-based).
func (c RestartConfig) ShouldRestart(policy RestartPolicy, cleanExit bool, attempt int) bool {
	cfg := c.Effective()
	switch policy {
	case RestartAlways:
		return attempt < cfg.MaxRetries
	case RestartOnFailure:
		return !cleanExit && attempt < cfg.MaxRetries
	default:
		return false
	}
}

// ParseRestartConfig reads restart settings from module config env keys.
// Supported keys: restart_policy, restart_max_retries, restart_initial_backoff,
// restart_max_backoff (Go duration strings, e.g. "5s", "1m").
func ParseRestartConfig(policy RestartPolicy, config map[string]string) (RestartPolicy, RestartConfig) {
	cfg := DefaultRestartConfig
	if config == nil {
		return policy, cfg
	}
	if p := strings.TrimSpace(config["restart_policy"]); p != "" {
		switch RestartPolicy(p) {
		case RestartNever, RestartOnFailure, RestartAlways:
			policy = RestartPolicy(p)
		}
	}
	if v := strings.TrimSpace(config["restart_max_retries"]); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			cfg.MaxRetries = n
		}
	}
	if v := strings.TrimSpace(config["restart_initial_backoff"]); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			cfg.InitialBackoff = d
		}
	}
	if v := strings.TrimSpace(config["restart_max_backoff"]); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			cfg.MaxBackoff = d
		}
	}
	return policy, cfg
}
