package erasure

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"os"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode"
	"unicode/utf8"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	authv1 "github.com/Muxcore-Media/core/proto/gen/muxcore/auth/v1"
)

// EnvSweepInterval is the environment variable read when
// Config.Interval is zero: a Go duration such as "5m".
const EnvSweepInterval = "ERASURE_SWEEP_INTERVAL"

// Sweep interval defaults and bounds. Configured values outside
// [MinInterval, MaxInterval] are clamped.
const (
	DefaultInterval = 5 * time.Minute
	MinInterval     = 30 * time.Second
	MaxInterval     = 24 * time.Hour
)

// Other defaults.
const (
	DefaultJitter       = 0.2
	DefaultPageSize     = 100
	MaxPageSize         = 500
	DefaultMaxEntries   = 100_000
	DefaultCallTimeout  = 30 * time.Second
	DefaultAckAttempts  = 5
	DefaultAckBackoff   = 500 * time.Millisecond
	maxAckBackoff       = 10 * time.Second
	DefaultRetryBackoff = 15 * time.Second
	maxCountsEntries    = 32
	maxIDLen            = 256
)

// ErrProviderUnsupported is returned by a sweep when the identity provider
// answers Unimplemented: it has no erasure ledger (e.g. auth-oidc, ADR-0035
// §7). Nothing is erased and the status is StateUnsupported.
var ErrProviderUnsupported = errors.New("erasure: identity provider does not implement the erasure ledger")

// ErrAlreadyRunning is returned by Run when it is already running.
var ErrAlreadyRunning = errors.New("erasure: reconciler already running")

// State summarises the last sweep.
type State string

// States.
const (
	StateNever       State = "never"       // no sweep has finished yet
	StateOK          State = "ok"          // the whole ledger was read and processed
	StateError       State = "error"       // the sweep failed or some tombstones did
	StateUnsupported State = "unsupported" // the provider has no ledger
)

// Config configures a Reconciler.
type Config struct {
	// Owner is the module's personal-data store. Required.
	Owner Owner
	// Dialer reaches the identity provider. Required.
	Dialer *ProviderDialer
	// Logger defaults to slog.Default().
	Logger *slog.Logger
	// Getenv defaults to os.Getenv (ERASURE_SWEEP_INTERVAL).
	Getenv func(string) string
	// Interval between sweeps. Zero reads ERASURE_SWEEP_INTERVAL, else
	// DefaultInterval. Clamped to [MinInterval, MaxInterval].
	Interval time.Duration
	// Jitter is the +/- fraction applied to every delay; default 0.2, at
	// most 0.5. A negative value disables jitter.
	Jitter float64
	// CallTimeout bounds each RPC; default 30s.
	CallTimeout time.Duration
	// AckBackoff is the first delay between acknowledgement attempts;
	// default 500ms, doubling up to 10s.
	AckBackoff time.Duration
	// RetryBackoff is the first delay before retrying a failed sweep
	// (provider down); default 15s, doubling up to Interval.
	RetryBackoff time.Duration
	// MaxEntries bounds the ledger size one sweep processes; a larger
	// ledger fails the sweep. Default 100000.
	MaxEntries int
	// AckAttempts bounds acknowledgement attempts per tombstone per sweep;
	// default 5. Unacknowledged tombstones are re-acknowledged next sweep.
	AckAttempts int
	// PageSize is the ListUserErasures page size; default 100, max 500.
	PageSize int32
}

// Result describes one sweep.
type Result struct {
	// Seen counts tombstones listed.
	Seen int
	// Applied counts tombstones applied in this sweep.
	Applied int
	// Acked counts acknowledgements accepted by the provider.
	Acked int
	// Failed counts tombstones acknowledged FAILED or UNSUPPORTED.
	Failed int
	// Skipped counts tombstones already applied and acknowledged OK.
	Skipped int
	// Errors counts tombstones left for the next sweep (owner error,
	// acknowledgement not accepted, invalid tombstone).
	Errors int
}

// Status is a snapshot of the reconciler for health and CLI output.
type Status struct {
	LastSweep time.Time
	State     State
	LastError string
	Provider  string
	Last      Result
	Sweeps    int64
}

// Reconciler applies the provider's erasure ledger to one Owner.
type Reconciler struct {
	owner   Owner
	dialer  *ProviderDialer
	log     *slog.Logger
	trigger chan struct{}
	jitterR func() float64
	sleep   func(ctx context.Context, d time.Duration) bool
	erased  map[string]struct{}
	status  Status
	cfg     Config

	sweepMu           sync.Mutex // one sweep at a time
	mu                sync.Mutex // status, erased
	running           atomic.Bool
	unsupportedLogged bool
}

// New validates cfg and returns a Reconciler.
func New(cfg Config) (*Reconciler, error) {
	if cfg.Owner == nil {
		return nil, errors.New("erasure: Config.Owner is required")
	}
	if strings.TrimSpace(cfg.Owner.ModuleID()) == "" {
		return nil, errors.New("erasure: Owner.ModuleID() is empty")
	}
	if cfg.Dialer == nil || cfg.Dialer.Discovery == nil {
		return nil, errors.New("erasure: Config.Dialer with Discovery is required")
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	getenv := cfg.Getenv
	if getenv == nil {
		getenv = os.Getenv
	}
	if cfg.Interval == 0 {
		iv, err := IntervalFromEnv(getenv)
		if err != nil {
			return nil, err
		}
		cfg.Interval = iv
	}
	if c := ClampInterval(cfg.Interval); c != cfg.Interval {
		cfg.Logger.Warn("erasure: sweep interval clamped", "requested", cfg.Interval, "interval", c)
		cfg.Interval = c
	}
	switch {
	case cfg.Jitter == 0:
		cfg.Jitter = DefaultJitter
	case cfg.Jitter < 0:
		cfg.Jitter = 0
	case cfg.Jitter > 0.5:
		cfg.Jitter = 0.5
	}
	if cfg.PageSize <= 0 {
		cfg.PageSize = DefaultPageSize
	}
	cfg.PageSize = min(cfg.PageSize, MaxPageSize)
	if cfg.MaxEntries <= 0 {
		cfg.MaxEntries = DefaultMaxEntries
	}
	if cfg.CallTimeout <= 0 {
		cfg.CallTimeout = DefaultCallTimeout
	}
	if cfg.AckAttempts <= 0 {
		cfg.AckAttempts = DefaultAckAttempts
	}
	if cfg.AckBackoff <= 0 {
		cfg.AckBackoff = DefaultAckBackoff
	}
	if cfg.RetryBackoff <= 0 {
		cfg.RetryBackoff = DefaultRetryBackoff
	}
	return &Reconciler{
		owner:   cfg.Owner,
		dialer:  cfg.Dialer,
		log:     cfg.Logger.With("module", cfg.Owner.ModuleID()),
		trigger: make(chan struct{}, 1),
		jitterR: rand.Float64, //nolint:gosec // G404: scheduling jitter, not a secret
		sleep:   sleepCtx,
		erased:  map[string]struct{}{},
		status:  Status{State: StateNever},
		cfg:     cfg,
	}, nil
}

// IntervalFromEnv parses ERASURE_SWEEP_INTERVAL (a Go duration). Unset or
// blank returns DefaultInterval; an unparsable or non-positive value is an
// error. The result is not clamped (see ClampInterval).
func IntervalFromEnv(getenv func(string) string) (time.Duration, error) {
	if getenv == nil {
		getenv = os.Getenv
	}
	v := strings.TrimSpace(getenv(EnvSweepInterval))
	if v == "" {
		return DefaultInterval, nil
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return 0, fmt.Errorf("erasure: %s=%q: %w", EnvSweepInterval, v, err)
	}
	if d <= 0 {
		return 0, fmt.Errorf("erasure: %s=%q must be positive", EnvSweepInterval, v)
	}
	return d, nil
}

// ClampInterval bounds d to [MinInterval, MaxInterval].
func ClampInterval(d time.Duration) time.Duration {
	return min(max(d, MinInterval), MaxInterval)
}

// jittered returns d scaled by a uniform factor in [1-frac, 1+frac].
func jittered(d time.Duration, frac, r float64) time.Duration {
	if frac <= 0 || d <= 0 {
		return d
	}
	return time.Duration(float64(d) * (1 - frac + 2*frac*r))
}

func sleepCtx(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

// Trigger requests a sweep as soon as possible. It never blocks; triggers
// that arrive while one is pending are coalesced. It only has an effect
// while Run is running.
func (r *Reconciler) Trigger() {
	select {
	case r.trigger <- struct{}{}:
	default:
	}
}

// Status returns a snapshot of the last sweep.
func (r *Reconciler) Status() Status {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.status
}

// Erased reports whether userID appeared in a ledger listing this process
// has read. Owners that write on behalf of a user id without that user's
// bearer use it to refuse erased ids (ADR-0035 §3). False before the first
// listing: it is never proof that a user is not erased.
func (r *Reconciler) Erased(userID string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	_, ok := r.erased[userID]
	return ok
}

// Run sweeps at startup, then every jittered interval and on Trigger, until
// ctx is cancelled. After a failed sweep (provider down) it retries with a
// jittered exponential backoff starting at RetryBackoff and capped at the
// interval. It returns ctx.Err() and leaves no goroutine behind.
func (r *Reconciler) Run(ctx context.Context) error {
	if !r.running.CompareAndSwap(false, true) {
		return ErrAlreadyRunning
	}
	defer r.running.Store(false)
	failures := 0
	for {
		_, err := r.SweepOnce(ctx)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil && !errors.Is(err, ErrProviderUnsupported) {
			failures++
		} else {
			failures = 0
		}
		t := time.NewTimer(r.nextDelay(failures))
		select {
		case <-ctx.Done():
			t.Stop()
			return ctx.Err()
		case <-t.C:
		case <-r.trigger:
			t.Stop()
		}
	}
}

// nextDelay is the jittered delay before the next sweep after `failures`
// consecutive failed sweeps.
func (r *Reconciler) nextDelay(failures int) time.Duration {
	d := r.cfg.Interval
	if failures > 0 {
		d = r.cfg.RetryBackoff
		for i := 1; i < failures && d < r.cfg.Interval; i++ {
			d *= 2
		}
		d = min(d, r.cfg.Interval)
	}
	return jittered(d, r.cfg.Jitter, r.jitterR())
}

// SweepOnce reads the whole ledger once and processes every tombstone. It
// is safe to call concurrently with Run and itself; sweeps are serialised.
// The returned error is nil only if every tombstone is applied and
// acknowledged (or was already).
func (r *Reconciler) SweepOnce(ctx context.Context) (Result, error) {
	r.sweepMu.Lock()
	defer r.sweepMu.Unlock()
	res, provider, err := r.sweep(ctx)
	r.finish(res, provider, err)
	return res, err
}

func (r *Reconciler) finish(res Result, provider string, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.status.Sweeps++
	r.status.LastSweep = time.Now().UTC()
	r.status.Last = res
	if provider != "" {
		r.status.Provider = provider
	}
	switch {
	case errors.Is(err, ErrProviderUnsupported):
		r.status.State, r.status.LastError = StateUnsupported, ""
		if !r.unsupportedLogged {
			r.unsupportedLogged = true
			r.log.Warn("erasure: the identity provider has no erasure ledger; no erasures are applied", "provider", provider)
		}
	case err != nil:
		r.status.State, r.status.LastError = StateError, err.Error()
		r.log.Warn("erasure: sweep incomplete", "error", err,
			"seen", res.Seen, "applied", res.Applied, "acked", res.Acked, "failed", res.Failed, "errors", res.Errors)
	default:
		r.status.State, r.status.LastError = StateOK, ""
		r.unsupportedLogged = false
		if res.Applied > 0 || res.Acked > 0 {
			r.log.Info("erasure: sweep complete",
				"seen", res.Seen, "applied", res.Applied, "acked", res.Acked, "failed", res.Failed)
		}
	}
}

func (r *Reconciler) sweep(ctx context.Context) (Result, string, error) {
	var res Result
	conn, err := r.dialer.Dial(ctx, r.owner.ModuleID())
	if err != nil {
		return res, "", err
	}
	defer func() { _ = conn.Close() }()
	provider := conn.Provider.ModuleID

	token := ""
	maxPages := r.cfg.MaxEntries/int(r.cfg.PageSize) + 2
	for page := 0; ; page++ {
		if page >= maxPages {
			return res, provider, fmt.Errorf("erasure: ledger exceeds %d pages", maxPages)
		}
		callCtx, cancel := context.WithTimeout(ctx, r.cfg.CallTimeout)
		resp, err := conn.Client.ListUserErasures(callCtx, &authv1.ListUserErasuresRequest{PageToken: token, PageSize: r.cfg.PageSize})
		cancel()
		if err != nil {
			if status.Code(err) == codes.Unimplemented {
				return res, provider, ErrProviderUnsupported
			}
			return res, provider, fmt.Errorf("erasure: list ledger from %s: %w", provider, err)
		}
		if len(resp.GetErasures()) > int(r.cfg.PageSize) {
			return res, provider, fmt.Errorf("erasure: provider returned %d tombstones for page size %d", len(resp.GetErasures()), r.cfg.PageSize)
		}
		for _, e := range resp.GetErasures() {
			if err := ctx.Err(); err != nil {
				return res, provider, err
			}
			res.Seen++
			if res.Seen > r.cfg.MaxEntries {
				return res, provider, fmt.Errorf("erasure: ledger exceeds %d entries", r.cfg.MaxEntries)
			}
			r.process(ctx, conn.Client, e, &res)
		}
		next := resp.GetNextPageToken()
		if next == "" {
			break
		}
		if next == token {
			return res, provider, errors.New("erasure: ledger page token did not advance")
		}
		token = next
	}
	if err := ctx.Err(); err != nil {
		return res, provider, err
	}
	if res.Errors > 0 || res.Failed > 0 {
		return res, provider, fmt.Errorf("erasure: %d tombstones failed, %d left for the next sweep", res.Failed, res.Errors)
	}
	return res, provider, nil
}

var erasureIDRE = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,128}$`)

func validField(s string, required bool) bool {
	if s == "" {
		return !required
	}
	if len(s) > maxIDLen || !utf8.ValidString(s) || strings.TrimSpace(s) != s {
		return false
	}
	for _, c := range s {
		if unicode.IsControl(c) {
			return false
		}
	}
	return true
}

func tombstoneFrom(e *authv1.UserErasure) (Tombstone, error) {
	if !erasureIDRE.MatchString(e.GetErasureId()) {
		return Tombstone{}, errors.New("invalid erasure_id")
	}
	if !validField(e.GetUserId(), true) {
		return Tombstone{}, errors.New("invalid user_id")
	}
	if !validField(e.GetTenantId(), false) {
		return Tombstone{}, errors.New("invalid tenant_id")
	}
	at, err := time.Parse(time.RFC3339Nano, e.GetDeletedAt())
	if err != nil {
		return Tombstone{}, errors.New("invalid deleted_at")
	}
	return Tombstone{ErasureID: e.GetErasureId(), UserID: e.GetUserId(), TenantID: e.GetTenantId(), DeletedAt: at.UTC()}, nil
}

// redact removes the user id from owner error text before it is logged.
func redact(err error, t Tombstone) string {
	return strings.ReplaceAll(err.Error(), t.UserID, "<user>")
}

func (r *Reconciler) process(ctx context.Context, client authv1.AuthServiceClient, e *authv1.UserErasure, res *Result) {
	t, err := tombstoneFrom(e)
	if err != nil {
		res.Errors++
		if erasureIDRE.MatchString(e.GetErasureId()) {
			r.log.Warn("erasure: invalid tombstone from provider; not applied", "erasure_id", e.GetErasureId(), "error", err)
			if r.ack(ctx, client, e.GetErasureId(), authv1.ErasureOutcome_ERASURE_OUTCOME_FAILED, DetailInvalidTombstone, nil) == nil {
				res.Acked++
			}
		} else {
			r.log.Warn("erasure: tombstone without a valid erasure id from provider; not applied", "error", err)
		}
		return
	}
	r.mu.Lock()
	r.erased[t.UserID] = struct{}{}
	r.mu.Unlock()

	log := r.log.With("erasure_id", t.ErasureID)
	applied, err := r.owner.Applied(ctx, t.ErasureID)
	if err != nil {
		res.Errors++
		log.Warn("erasure: cannot read the local applied record; retrying next sweep", "error", redact(err, t))
		return
	}
	var counts Counts
	if applied {
		if e.GetAcknowledgedByCaller() {
			res.Skipped++
			return
		}
	} else {
		counts, err = r.owner.Apply(ctx, t)
		if err != nil {
			if ctx.Err() != nil {
				res.Errors++
				return
			}
			outcome, code := authv1.ErasureOutcome_ERASURE_OUTCOME_FAILED, detailCode(err)
			if errors.Is(err, ErrUnsupported) {
				outcome, code = authv1.ErasureOutcome_ERASURE_OUTCOME_UNSUPPORTED, DetailUnsupported
			}
			res.Failed++
			log.Warn("erasure: apply failed; retrying next sweep", "detail", code, "error", redact(err, t))
			if r.ack(ctx, client, t.ErasureID, outcome, code, nil) == nil {
				res.Acked++
			}
			return
		}
		res.Applied++
	}
	outcome, code := r.verify(ctx, t, log)
	if ctx.Err() != nil {
		res.Errors++
		return
	}
	if outcome != authv1.ErasureOutcome_ERASURE_OUTCOME_OK {
		res.Failed++
	}
	if err := r.ack(ctx, client, t.ErasureID, outcome, code, counts); err != nil {
		res.Errors++
		log.Warn("erasure: acknowledgement not accepted; re-sent next sweep", "error", err)
		return
	}
	res.Acked++
}

func (r *Reconciler) verify(ctx context.Context, t Tombstone, log *slog.Logger) (authv1.ErasureOutcome, string) {
	v, ok := r.owner.(Verifier)
	if !ok {
		return authv1.ErasureOutcome_ERASURE_OUTCOME_OK, ""
	}
	remaining, err := v.Verify(ctx, t)
	if err != nil {
		log.Warn("erasure: post-condition check failed", "error", redact(err, t))
		return authv1.ErasureOutcome_ERASURE_OUTCOME_FAILED, DetailVerifyFailed
	}
	if remaining != 0 {
		log.Warn("erasure: post-condition unmet after application", "remaining", remaining)
		return authv1.ErasureOutcome_ERASURE_OUTCOME_FAILED, DetailPostconditionUnmet
	}
	return authv1.ErasureOutcome_ERASURE_OUTCOME_OK, ""
}

func sanitizeCounts(c Counts) map[string]int64 {
	if len(c) == 0 {
		return nil
	}
	out := make(map[string]int64, min(len(c), maxCountsEntries))
	for k, v := range c {
		if len(out) == maxCountsEntries {
			break
		}
		if ValidDetailCode(k) && v >= 0 {
			out[k] = v
		}
	}
	return out
}

func retryable(c codes.Code) bool {
	switch c {
	case codes.Unavailable, codes.DeadlineExceeded, codes.ResourceExhausted, codes.Aborted, codes.Internal, codes.Unknown:
		return true
	}
	return false
}

// ack sends one acknowledgement, retrying transient failures with a
// jittered exponential backoff. Acknowledgements are idempotent.
func (r *Reconciler) ack(ctx context.Context, client authv1.AuthServiceClient, erasureID string, outcome authv1.ErasureOutcome, code string, counts Counts) error {
	req := &authv1.AckUserErasureRequest{ErasureId: erasureID, Outcome: outcome, DetailCode: code, Counts: sanitizeCounts(counts)}
	backoff := r.cfg.AckBackoff
	for attempt := 1; ; attempt++ {
		callCtx, cancel := context.WithTimeout(ctx, r.cfg.CallTimeout)
		_, err := client.AckUserErasure(callCtx, req)
		cancel()
		if err == nil {
			return nil
		}
		if attempt >= r.cfg.AckAttempts || !retryable(status.Code(err)) || ctx.Err() != nil {
			return fmt.Errorf("erasure: acknowledge %s: %w", erasureID, err)
		}
		if !r.sleep(ctx, jittered(backoff, r.cfg.Jitter, r.jitterR())) {
			return fmt.Errorf("erasure: acknowledge %s: %w", erasureID, ctx.Err())
		}
		backoff = min(backoff*2, maxAckBackoff)
	}
}
