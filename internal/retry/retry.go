package retry

import (
	"context"
	"encoding/binary"
	"fmt"
	"log/slog"
	"math/rand"
	"strings"
	"sync"
	"time"

	cryptorand "crypto/rand"

	"github.com/Muxcore-Media/core/internal/registry"
	"github.com/Muxcore-Media/core/pkg/contracts"
)

var (
	rngMu sync.Mutex
	rng   = func() *rand.Rand {
		var seed int64
		if err := binary.Read(cryptorand.Reader, binary.LittleEndian, &seed); err != nil {
			seed = 42
		}
		return rand.New(rand.NewSource(seed)) //nolint:gosec // non-crypto use for jitter in retry backoff
	}()
)

const (
	defaultMaxAttempts   = 3
	defaultInitialDelay  = 100 * time.Millisecond
	defaultMaxDelay      = 30 * time.Second
	defaultBackoffFactor = 2.0
)

func normalizePolicy(p contracts.RetryPolicy) contracts.RetryPolicy {
	if p.MaxAttempts <= 0 {
		p.MaxAttempts = defaultMaxAttempts
	}
	if p.InitialDelay <= 0 {
		p.InitialDelay = defaultInitialDelay
	}
	if p.MaxDelay <= 0 {
		p.MaxDelay = defaultMaxDelay
	}
	if p.BackoffFactor <= 0 {
		p.BackoffFactor = defaultBackoffFactor
	}
	return p
}

// Execute runs fn with configurable retry and backoff.
func Execute(ctx context.Context, policy contracts.RetryPolicy, fn func(ctx context.Context) error) error {
	if fn == nil {
		return fmt.Errorf("retry: fn is nil")
	}
	p := normalizePolicy(policy)

	var lastErr error
	delay := p.InitialDelay

	for attempt := 0; attempt < p.MaxAttempts; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(delay):
			}
		}

		if err := fn(ctx); err != nil {
			lastErr = err
			delay = calcNextDelay(delay, p)
			continue
		}
		return nil
	}

	return lastErr
}

func calcNextDelay(current time.Duration, p contracts.RetryPolicy) time.Duration {
	next := time.Duration(float64(current) * p.BackoffFactor)
	if p.MaxDelay > 0 && next > p.MaxDelay {
		next = p.MaxDelay
	}
	if p.Jitter {
		rngMu.Lock()
		jitter := time.Duration(rng.Float64() * float64(next))
		rngMu.Unlock()
		next = next/2 + jitter
	}
	if next <= 0 {
		next = p.InitialDelay
	}
	return next
}

// Provider implements contracts.RetryProvider using exponential backoff.
type Provider struct{}

func NewProvider() *Provider {
	return &Provider{}
}

func (p *Provider) Execute(ctx context.Context, policy contracts.RetryPolicy, fn func(ctx context.Context) error) error {
	return Execute(ctx, policy, fn)
}

var _ contracts.RetryProvider = (*Provider)(nil)

// retryModule wraps a Provider to satisfy contracts.Module for registry registration.
type retryModule struct {
	provider *Provider
	nodeID   string
}

func (m *retryModule) Info() contracts.ModuleInfo {
	return contracts.ModuleInfo{
		ID:           "core.retry",
		Name:         "core-retry",
		Version:      "1.0.0",
		Description:  "Core exponential backoff retry provider for transient failure handling",
		Capabilities: []string{contracts.CapabilityRetry},
	}
}

func (m *retryModule) Init(_ context.Context) error   { return nil }
func (m *retryModule) Start(_ context.Context) error  { return nil }
func (m *retryModule) Stop(_ context.Context) error   { return nil }
func (m *retryModule) Health(_ context.Context) error { return nil }

// RegisterSelf creates a virtual module entry in the given Registry so
// that this retry provider is discoverable via FindByCapability("retry").
func RegisterSelf(reg *registry.Registry, provider *Provider, nodeID string) {
	if reg == nil {
		slog.Warn("retry: RegisterSelf called with nil registry")
		return
	}
	m := &retryModule{provider: provider, nodeID: nodeID}
	if err := reg.Register(m, nil); err != nil && !strings.Contains(err.Error(), "already registered") {
		slog.Warn("retry: register self", "error", err)
	}
}
