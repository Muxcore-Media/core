package retry

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Muxcore-Media/core/internal/registry"
	"github.com/Muxcore-Media/core/pkg/contracts"
)

func TestExecute_SucceedsFirstAttempt(t *testing.T) {
	var attempts int
	err := Execute(context.Background(), contracts.RetryPolicy{}, func(_ context.Context) error {
		attempts++
		return nil
	})
	if err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
	if attempts != 1 {
		t.Fatalf("expected 1 attempt, got %d", attempts)
	}
}

func TestExecute_RetriesUntilSuccess(t *testing.T) {
	var attempts int
	err := Execute(context.Background(), contracts.RetryPolicy{
		MaxAttempts:   5,
		InitialDelay:  1 * time.Millisecond,
		BackoffFactor: 1.0,
	}, func(_ context.Context) error {
		attempts++
		if attempts < 3 {
			return errors.New("transient error")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
	if attempts != 3 {
		t.Fatalf("expected 3 attempts, got %d", attempts)
	}
}

func TestExecute_FailsAfterMaxAttempts(t *testing.T) {
	var attempts int
	err := Execute(context.Background(), contracts.RetryPolicy{
		MaxAttempts:   3,
		InitialDelay:  1 * time.Millisecond,
		BackoffFactor: 1.0,
	}, func(_ context.Context) error {
		attempts++
		return errors.New("persistent error")
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if attempts != 3 {
		t.Fatalf("expected 3 attempts, got %d", attempts)
	}
}

func TestExecute_NilFn(t *testing.T) {
	err := Execute(context.Background(), contracts.RetryPolicy{}, nil)
	if err == nil {
		t.Fatal("expected error for nil fn")
	}
}

func TestExecute_ContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := Execute(ctx, contracts.RetryPolicy{
		MaxAttempts:   10,
		InitialDelay:  10 * time.Millisecond,
		BackoffFactor: 1.0,
	}, func(_ context.Context) error {
		return errors.New("should not retry")
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}

func TestExecute_ContextCancelledBetweenRetries(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	var attempts int
	err := Execute(ctx, contracts.RetryPolicy{
		MaxAttempts:   10,
		InitialDelay:  50 * time.Millisecond,
		BackoffFactor: 1.0,
	}, func(_ context.Context) error {
		attempts++
		if attempts == 1 {
			cancel()
		}
		return errors.New("fail")
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
	if attempts > 2 {
		t.Fatalf("expected at most 2 attempts, got %d", attempts)
	}
}

func TestNormalizePolicy_Defaults(t *testing.T) {
	p := normalizePolicy(contracts.RetryPolicy{})
	if p.MaxAttempts != defaultMaxAttempts {
		t.Fatalf("expected %d attempts, got %d", defaultMaxAttempts, p.MaxAttempts)
	}
	if p.InitialDelay != defaultInitialDelay {
		t.Fatalf("expected %s delay, got %s", defaultInitialDelay, p.InitialDelay)
	}
	if p.MaxDelay != defaultMaxDelay {
		t.Fatalf("expected %s max delay, got %s", defaultMaxDelay, p.MaxDelay)
	}
	if p.BackoffFactor != defaultBackoffFactor {
		t.Fatalf("expected %f factor, got %f", defaultBackoffFactor, p.BackoffFactor)
	}
}

func TestNormalizePolicy_PreservesNonZero(t *testing.T) {
	input := contracts.RetryPolicy{
		MaxAttempts:   10,
		InitialDelay:  500 * time.Millisecond,
		MaxDelay:      1 * time.Minute,
		BackoffFactor: 3.0,
		Jitter:        true,
	}
	p := normalizePolicy(input)
	if p.MaxAttempts != 10 {
		t.Fatalf("expected 10, got %d", p.MaxAttempts)
	}
	if p.InitialDelay != 500*time.Millisecond {
		t.Fatalf("expected 500ms, got %s", p.InitialDelay)
	}
	if p.MaxDelay != 1*time.Minute {
		t.Fatalf("expected 1m, got %s", p.MaxDelay)
	}
	if p.BackoffFactor != 3.0 {
		t.Fatalf("expected 3.0, got %f", p.BackoffFactor)
	}
	if !p.Jitter {
		t.Fatal("expected Jitter to be true")
	}
}

func TestCalcNextDelay_BackoffFactor(t *testing.T) {
	p := contracts.RetryPolicy{
		InitialDelay:  10 * time.Millisecond,
		MaxDelay:      10 * time.Second,
		BackoffFactor: 2.0,
	}
	next := calcNextDelay(10*time.Millisecond, p)
	if next != 20*time.Millisecond {
		t.Fatalf("expected 20ms, got %s", next)
	}
	next2 := calcNextDelay(next, p)
	if next2 != 40*time.Millisecond {
		t.Fatalf("expected 40ms, got %s", next2)
	}
}

func TestCalcNextDelay_CapsAtMaxDelay(t *testing.T) {
	p := contracts.RetryPolicy{
		InitialDelay:  10 * time.Millisecond,
		MaxDelay:      25 * time.Millisecond,
		BackoffFactor: 3.0,
	}
	next := calcNextDelay(10*time.Millisecond, p)
	if next != 25*time.Millisecond {
		t.Fatalf("expected cap at 25ms, got %s", next)
	}
}

func TestCalcNextDelay_Jitter(t *testing.T) {
	p := contracts.RetryPolicy{
		InitialDelay:  10 * time.Millisecond,
		MaxDelay:      10 * time.Second,
		BackoffFactor: 2.0,
		Jitter:        true,
	}
	base := 100 * time.Millisecond
	expected := time.Duration(float64(base) * 2.0)
	seen := make(map[time.Duration]bool)
	for i := 0; i < 50; i++ {
		next := calcNextDelay(base, p)
		seen[next] = true
		lower := expected / 2
		upper := lower + expected
		if next < lower || next >= upper {
			t.Fatalf("jittered delay %s out of range [%s, %s)", next, lower, upper)
		}
	}
	if len(seen) < 2 {
		t.Fatal("expected jitter to produce varying delays")
	}
}

func TestCalcNextDelay_NeverZero(t *testing.T) {
	p := contracts.RetryPolicy{
		InitialDelay:  10 * time.Millisecond,
		MaxDelay:      10 * time.Second,
		BackoffFactor: 0.0,
	}
	p = normalizePolicy(p)
	next := calcNextDelay(10*time.Millisecond, p)
	if next <= 0 {
		t.Fatalf("expected positive delay, got %s", next)
	}
}

func TestProvider_Execute_Delegates(t *testing.T) {
	p := NewProvider()
	var called bool
	err := p.Execute(context.Background(), contracts.RetryPolicy{
		MaxAttempts: 1,
	}, func(_ context.Context) error {
		called = true
		return nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !called {
		t.Fatal("fn was not called")
	}
}

func TestNewProvider_ReturnsNonNil(t *testing.T) {
	p := NewProvider()
	if p == nil {
		t.Fatal("expected non-nil provider")
	}
}

func TestProvider_ImplementsInterface(t *testing.T) {
	var _ contracts.RetryProvider = (*Provider)(nil)
}

func TestRegisterSelf_RegistersInRegistry(t *testing.T) {
	reg := registry.New()
	prov := NewProvider()
	RegisterSelf(reg, prov, "node-1")

	caps := reg.FindByCapability(contracts.CapabilityRetry)
	if len(caps) == 0 {
		t.Fatal("expected retry module to be registered")
	}
	found := false
	for _, e := range caps {
		if e.Info.ID == "core.retry" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("expected to find core.retry module")
	}
}

func TestRegisterSelf_NilRegistry(t *testing.T) {
	prov := NewProvider()
	RegisterSelf(nil, prov, "node-1")
}

func TestRegisterSelf_Idempotent(t *testing.T) {
	reg := registry.New()
	prov := NewProvider()
	RegisterSelf(reg, prov, "node-1")
	RegisterSelf(reg, prov, "node-1")
}

func TestRetryModule_Info(t *testing.T) {
	m := &retryModule{provider: NewProvider(), nodeID: "test-node"}
	info := m.Info()
	if info.ID != "core.retry" {
		t.Fatalf("expected ID core.retry, got %s", info.ID)
	}
	if info.Name != "core-retry" {
		t.Fatalf("expected Name core-retry, got %s", info.Name)
	}
	if info.Version != "1.0.0" {
		t.Fatalf("expected Version 1.0.0, got %s", info.Version)
	}
	if len(info.Capabilities) != 1 || info.Capabilities[0] != contracts.CapabilityRetry {
		t.Fatalf("unexpected capabilities: %v", info.Capabilities)
	}
}

func TestRetryModule_Lifecycle(t *testing.T) {
	m := &retryModule{provider: NewProvider(), nodeID: "test-node"}
	ctx := context.Background()
	if err := m.Init(ctx); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := m.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := m.Health(ctx); err != nil {
		t.Fatalf("Health: %v", err)
	}
	if err := m.Stop(ctx); err != nil {
		t.Fatalf("Stop: %v", err)
	}
}
