package module

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	healthv1 "github.com/Muxcore-Media/core/proto/gen/muxcore/health/v1"
	modulev1 "github.com/Muxcore-Media/core/proto/gen/muxcore/module/v1"
)

const (
	// defaultSupervisionInterval is how often Run checks that core still
	// knows the module.
	defaultSupervisionInterval = 15 * time.Second
	// maxProbeTimeout bounds one supervision probe.
	maxProbeTimeout = 5 * time.Second
	// maxReregisterBackoff caps the wait between failed re-registrations.
	maxReregisterBackoff = time.Minute
	// unregisterTimeout bounds the best-effort Unregister on shutdown.
	unregisterTimeout = 3 * time.Second
)

// probeResult is what one supervision probe learned about core.
type probeResult int

const (
	// probeKnown: core is reachable and has the module registered.
	probeKnown probeResult = iota
	// probeUnknown: core is reachable and does not know the module (it
	// restarted, or the registration was removed).
	probeUnknown
	// probeUnreachable: core could not be reached.
	probeUnreachable
	// probeUnsupported: core is reachable but cannot answer the question
	// (older core, or the call was refused).
	probeUnsupported
)

func (r probeResult) String() string {
	switch r {
	case probeKnown:
		return "known"
	case probeUnknown:
		return "unknown"
	case probeUnreachable:
		return "unreachable"
	default:
		return "unsupported"
	}
}

// supervisor keeps the module registered while it runs: every interval it
// asks core whether the module is still registered and re-registers when
// core has forgotten it (core restarted or was recreated). When core cannot
// say (probeUnsupported), it re-registers once after core comes back from
// being unreachable. Failed re-registrations back off exponentially up to
// maxReregisterBackoff.
type supervisor struct {
	probe    func(ctx context.Context) probeResult
	register func(ctx context.Context) error
	moduleID string
	interval time.Duration
}

func (s *supervisor) run(ctx context.Context) {
	wait := s.interval
	lost := false // core was unreachable since the last good registration
	need := false // a re-registration is pending
	failures := 0 // consecutive failed re-registrations
	timer := time.NewTimer(wait)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}

		res := s.probe(ctx)
		if ctx.Err() != nil {
			return
		}
		switch res {
		case probeUnreachable:
			if !lost {
				slog.Warn("module: core unreachable; will re-register when it returns", "id", s.moduleID)
			}
			lost = true
		case probeUnknown:
			need = true
		case probeKnown:
			lost, need, failures = false, false, 0
		case probeUnsupported:
			if lost {
				need = true
			}
		}

		wait = s.interval
		if need && res != probeUnreachable {
			if err := s.register(ctx); err != nil {
				if ctx.Err() != nil {
					return
				}
				failures++
				wait = s.backoff(failures)
				slog.Warn("module: re-registration with core failed",
					"id", s.moduleID, "error", err, "retry_in", wait)
			} else {
				slog.Info("module: re-registered with core", "id", s.moduleID, "reason", res.String())
				lost, need, failures = false, false, 0
			}
		}
		timer.Reset(wait)
	}
}

// backoff returns the wait after n consecutive failures: min(interval, 1s)
// doubled per failure, capped at max(interval, maxReregisterBackoff).
func (s *supervisor) backoff(n int) time.Duration {
	base := min(s.interval, time.Second)
	limit := max(s.interval, maxReregisterBackoff)
	d := base
	for i := 1; i < n && d < limit; i++ {
		d *= 2
	}
	return min(d, limit)
}

// healthProbe asks core's HealthService whether moduleID is registered.
// Core answers STATUS_UNHEALTHY when the module ID does not resolve in its
// registry. x-caller-id identifies the module in the dev profile; with mTLS
// the certificate takes precedence.
//
// If core refuses the call (PermissionDenied, Unauthenticated) the probe
// stops calling it — repeated refusals would count as authentication
// failures for the module — and falls back to the connection state, which
// only tells reachable (probeUnsupported) from unreachable.
func healthProbe(conn *grpc.ClientConn, moduleID string, timeout time.Duration) func(context.Context) probeResult {
	hc := healthv1.NewHealthServiceClient(conn)
	refused := false
	return func(ctx context.Context) probeResult {
		if refused {
			conn.Connect()
			if conn.GetState() == connectivity.TransientFailure {
				return probeUnreachable
			}
			return probeUnsupported
		}
		ctx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		ctx = metadata.AppendToOutgoingContext(ctx, "x-caller-id", moduleID)
		resp, err := hc.Check(ctx, &healthv1.HealthCheckRequest{ModuleId: moduleID})
		if err != nil {
			switch status.Code(err) {
			case codes.Unavailable, codes.DeadlineExceeded, codes.Canceled:
				return probeUnreachable
			case codes.PermissionDenied, codes.Unauthenticated:
				refused = true
				slog.Warn("module: core refused the registration health probe; "+
					"supervision falls back to connection state", "id", moduleID, "error", err)
				return probeUnsupported
			default:
				return probeUnsupported
			}
		}
		if resp.GetStatus() == healthv1.HealthCheckResponse_STATUS_UNHEALTHY {
			return probeUnknown
		}
		return probeKnown
	}
}

// registerFunc returns a function that registers req with core. An
// "already registered" rejection counts as success: core knows the module.
func registerFunc(reg modulev1.ModuleRegistrationClient, req *modulev1.RegisterRequest, timeout time.Duration) func(context.Context) error {
	return func(ctx context.Context) error {
		ctx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		resp, err := reg.Register(ctx, req)
		if err != nil {
			return err
		}
		if !resp.GetAccepted() {
			if strings.Contains(resp.GetError(), "already registered") {
				return nil
			}
			return &rejectedError{msg: resp.GetError()}
		}
		return nil
	}
}

type rejectedError struct{ msg string }

func (e *rejectedError) Error() string { return "core rejected registration: " + e.msg }
