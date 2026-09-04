// Package circuitbreaker provides per-module circuit breakers for graceful
// degradation. It layers on top of the existing module degraded state without
// replacing it: OPEN mirrors degraded, HALF_OPEN probes recovery, CLOSED is healthy.
package circuitbreaker

import (
	"log/slog"
	"os"
	"strconv"
	"sync"
	"time"
)

// State is the circuit breaker state for a module.
type State string

const (
	// StateClosed — module is healthy; failures accumulate toward the trip threshold.
	StateClosed State = "CLOSED"
	// StateOpen — module is degraded; health probes are deferred until the probe interval elapses.
	StateOpen State = "OPEN"
	// StateHalfOpen — probing recovery; successes count toward restore, failures re-trip.
	StateHalfOpen State = "HALF_OPEN"
)

const (
	defaultProbeInterval     = 30 * time.Second
	defaultFailThreshold     = 3
	defaultRecoveryThreshold = 1
	envProbeInterval         = "CIRCUIT_BREAKER_PROBE_INTERVAL"
	envFailThreshold         = "CIRCUIT_BREAKER_FAIL_THRESHOLD"
	envRecoveryThreshold     = "CIRCUIT_BREAKER_RECOVERY_THRESHOLD"
)

// Config controls circuit breaker thresholds and probe timing.
type Config struct {
	ProbeInterval     time.Duration
	FailThreshold     int
	RecoveryThreshold int
}

// Effective returns config with zero values replaced by defaults.
func (c Config) Effective() Config {
	out := c
	if out.ProbeInterval <= 0 {
		out.ProbeInterval = defaultProbeInterval
	}
	if out.FailThreshold <= 0 {
		out.FailThreshold = defaultFailThreshold
	}
	if out.RecoveryThreshold <= 0 {
		out.RecoveryThreshold = defaultRecoveryThreshold
	}
	return out
}

// ConfigFromEnv reads circuit breaker settings from the environment.
func ConfigFromEnv() Config {
	cfg := Config{
		ProbeInterval:     defaultProbeInterval,
		FailThreshold:     defaultFailThreshold,
		RecoveryThreshold: defaultRecoveryThreshold,
	}
	if v := os.Getenv(envProbeInterval); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			cfg.ProbeInterval = d
		}
	}
	if v := os.Getenv(envFailThreshold); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cfg.FailThreshold = n
		}
	}
	if v := os.Getenv(envRecoveryThreshold); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cfg.RecoveryThreshold = n
		}
	}
	return cfg.Effective()
}

// Status is a point-in-time view of a module circuit breaker.
type Status struct {
	ModuleID          string    `json:"module_id"`
	State             State     `json:"state"`
	FailureCount      int       `json:"failure_count"`
	RecoveryCount     int       `json:"recovery_count"`
	OpenedAt          time.Time `json:"opened_at,omitempty"`
	LastTransitionAt  time.Time `json:"last_transition_at,omitempty"`
	ProbeInterval     string    `json:"probe_interval"`
	FailThreshold     int       `json:"fail_threshold"`
	RecoveryThreshold int       `json:"recovery_threshold"`
}

// Breaker tracks state for a single module.
type Breaker struct {
	mu             sync.RWMutex
	moduleID       string
	config         Config
	state          State
	failures       int
	recoveries     int
	openedAt       time.Time
	lastTransition time.Time
	now            func() time.Time
}

func newBreaker(moduleID string, cfg Config, now func() time.Time) *Breaker {
	if now == nil {
		now = time.Now
	}
	t := now()
	return &Breaker{
		moduleID:       moduleID,
		config:         cfg.Effective(),
		state:          StateClosed,
		lastTransition: t,
		now:            now,
	}
}

// State returns the current breaker state.
func (b *Breaker) State() State {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.state
}

// Status returns a snapshot of the breaker.
func (b *Breaker) Status() Status {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.statusLocked()
}

func (b *Breaker) statusLocked() Status {
	return Status{
		ModuleID:          b.moduleID,
		State:             b.state,
		FailureCount:      b.failures,
		RecoveryCount:     b.recoveries,
		OpenedAt:          b.openedAt,
		LastTransitionAt:  b.lastTransition,
		ProbeInterval:     b.config.ProbeInterval.String(),
		FailThreshold:     b.config.FailThreshold,
		RecoveryThreshold: b.config.RecoveryThreshold,
	}
}

func (b *Breaker) transition(to State) {
	from := b.state
	if from == to {
		return
	}
	b.state = to
	b.lastTransition = b.now()
	if to == StateOpen {
		b.openedAt = b.lastTransition
		b.recoveries = 0
	}
	if to == StateClosed {
		b.failures = 0
		b.recoveries = 0
		b.openedAt = time.Time{}
	}
	if to == StateHalfOpen {
		b.recoveries = 0
	}
	slog.Info("circuit breaker state transition",
		"module", b.moduleID,
		"from", from,
		"to", to,
	)
}

// MaybeAdvanceProbe transitions OPEN → HALF_OPEN when the probe interval has elapsed.
func (b *Breaker) MaybeAdvanceProbe() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.state != StateOpen || b.openedAt.IsZero() {
		return
	}
	if b.now().Sub(b.openedAt) >= b.config.ProbeInterval {
		b.transition(StateHalfOpen)
	}
}

// Trip forces the breaker to OPEN (e.g. when a module is marked degraded).
func (b *Breaker) Trip() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.state == StateOpen {
		return
	}
	b.failures = b.config.FailThreshold
	b.transition(StateOpen)
	slog.Warn("circuit breaker tripped",
		"module", b.moduleID,
		"reason", "degraded",
		"state", StateOpen,
	)
}

// RecordFailure records a health check failure.
// Returns true when the breaker trips to OPEN.
func (b *Breaker) RecordFailure() bool {
	b.mu.Lock()
	defer b.mu.Unlock()

	switch b.state {
	case StateClosed:
		b.failures++
		if b.failures >= b.config.FailThreshold {
			b.transition(StateOpen)
			slog.Warn("circuit breaker tripped",
				"module", b.moduleID,
				"reason", "failure_threshold",
				"failures", b.failures,
				"threshold", b.config.FailThreshold,
				"state", StateOpen,
			)
			return true
		}
	case StateHalfOpen:
		b.failures++
		b.transition(StateOpen)
		slog.Warn("circuit breaker tripped",
			"module", b.moduleID,
			"reason", "probe_failed",
			"state", StateOpen,
		)
		return true
	case StateOpen:
		// Already open; degradation logic handles state.
	}
	return false
}

// RecordSuccess records a health check success.
// Returns true when the breaker restores to CLOSED from HALF_OPEN.
func (b *Breaker) RecordSuccess() bool {
	b.mu.Lock()
	defer b.mu.Unlock()

	switch b.state {
	case StateClosed:
		b.failures = 0
	case StateHalfOpen:
		b.recoveries++
		if b.recoveries >= b.config.RecoveryThreshold {
			b.transition(StateClosed)
			slog.Info("circuit breaker recovered",
				"module", b.moduleID,
				"state", StateClosed,
				"recovery_count", b.recoveries,
			)
			return true
		}
	case StateOpen:
		// Probes are deferred until MaybeAdvanceProbe moves to HALF_OPEN.
	}
	return false
}

// Registry tracks per-module circuit breakers.
type Registry struct {
	mu       sync.RWMutex
	config   Config
	breakers map[string]*Breaker
	now      func() time.Time
}

// NewRegistry creates a circuit breaker registry.
func NewRegistry(cfg Config) *Registry {
	cfg = cfg.Effective()
	return &Registry{
		config:   cfg,
		breakers: make(map[string]*Breaker),
		now:      time.Now,
	}
}

// NewRegistryFromEnv creates a registry using environment configuration.
func NewRegistryFromEnv() *Registry {
	return NewRegistry(ConfigFromEnv())
}

// SetClock overrides the time source (tests).
func (r *Registry) SetClock(now func() time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.now = now
	for _, b := range r.breakers {
		b.now = now
	}
}

func (r *Registry) breaker(moduleID string) *Breaker {
	b, ok := r.breakers[moduleID]
	if !ok {
		now := r.now
		if now == nil {
			now = time.Now
		}
		b = newBreaker(moduleID, r.config, now)
		r.breakers[moduleID] = b
	}
	return b
}

// Trip opens the circuit for a module (degraded marking).
func (r *Registry) Trip(moduleID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.breaker(moduleID).Trip()
}

// RecordHealth records a health check result for a module.
func (r *Registry) RecordHealth(moduleID string, healthy bool) {
	r.mu.Lock()
	b := r.breaker(moduleID)
	r.mu.Unlock()

	b.MaybeAdvanceProbe()
	if healthy {
		b.RecordSuccess()
	} else {
		b.RecordFailure()
	}
}

// Snapshot returns status for one module.
func (r *Registry) Snapshot(moduleID string) Status {
	r.mu.RLock()
	b, ok := r.breakers[moduleID]
	r.mu.RUnlock()
	if !ok {
		cfg := r.config.Effective()
		return Status{
			ModuleID:          moduleID,
			State:             StateClosed,
			ProbeInterval:     cfg.ProbeInterval.String(),
			FailThreshold:     cfg.FailThreshold,
			RecoveryThreshold: cfg.RecoveryThreshold,
		}
	}
	return b.Status()
}

// SnapshotAll returns status for all modules with recorded breakers.
func (r *Registry) SnapshotAll() map[string]Status {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make(map[string]Status, len(r.breakers))
	for id, b := range r.breakers {
		out[id] = b.statusLocked()
	}
	return out
}
