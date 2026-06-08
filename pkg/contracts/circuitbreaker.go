package contracts

import (
	"context"
	"errors"
)

// ErrCircuitOpen is returned by CircuitBreaker.Execute when the circuit
// is open and the call is rejected without execution.
var ErrCircuitOpen = errors.New("circuit breaker is open")

// CircuitState represents the current state of a circuit breaker.
type CircuitState string

const (
	// CircuitClosed — normal operation, requests flow through.
	CircuitClosed CircuitState = "closed"
	// CircuitOpen — failing, requests are rejected immediately with ErrCircuitOpen.
	CircuitOpen CircuitState = "open"
	// CircuitHalfOpen — testing if the downstream has recovered.
	// A single success transitions back to closed; a failure re-opens.
	CircuitHalfOpen CircuitState = "half_open"
)

// CircuitBreaker protects downstream calls from cascading failures.
// When a downstream module degrades, the circuit opens and calls fail
// fast instead of waiting for timeouts. After a configurable cooldown
// (module-defined), the circuit transitions to half-open to test recovery.
//
// Modules that call other modules via Mesh.Call() wrap those calls in
// Execute() to gain circuit breaking without implementing it themselves.
type CircuitBreaker interface {
	// Execute runs fn within the circuit identified by key.
	// If the circuit is open, returns ErrCircuitOpen immediately.
	// If fn returns an error, the failure is recorded against the circuit.
	Execute(ctx context.Context, key string, fn func(context.Context) error) error

	// State returns the current state of the circuit identified by key.
	State(key string) CircuitState
}
