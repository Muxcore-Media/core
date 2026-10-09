package erasure

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	authv1 "github.com/Muxcore-Media/core/proto/gen/muxcore/auth/v1"
	discoveryv1 "github.com/Muxcore-Media/core/proto/gen/muxcore/discovery/v1"
	"google.golang.org/grpc"
)

type nopOwner struct{}

func (nopOwner) ModuleID() string                                 { return "owner" }
func (nopOwner) Applied(context.Context, string) (bool, error)    { return false, nil }
func (nopOwner) Apply(context.Context, Tombstone) (Counts, error) { return nil, nil }

type nopFinder struct{}

func (nopFinder) FindByCapability(context.Context, *discoveryv1.FindByCapabilityRequest, ...grpc.CallOption) (*discoveryv1.FindByCapabilityResponse, error) {
	return &discoveryv1.FindByCapabilityResponse{}, nil
}

func envMap(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func TestIntervalFromEnv(t *testing.T) {
	for _, tc := range []struct {
		in      string
		want    time.Duration
		wantErr bool
	}{
		{"", DefaultInterval, false},
		{"   ", DefaultInterval, false},
		{"5m", 5 * time.Minute, false},
		{" 90s ", 90 * time.Second, false},
		{"10s", 10 * time.Second, false}, // parsed as given; New clamps
		{"1h30m", 90 * time.Minute, false},
		{"300", 0, true}, // a bare number is not a duration
		{"abc", 0, true},
		{"0", 0, true},
		{"0s", 0, true},
		{"-1m", 0, true},
	} {
		got, err := IntervalFromEnv(envMap(map[string]string{EnvSweepInterval: tc.in}))
		if (err != nil) != tc.wantErr || got != tc.want {
			t.Errorf("IntervalFromEnv(%q) = %v, %v; want %v, err=%v", tc.in, got, err, tc.want, tc.wantErr)
		}
	}
}

func TestNewIntervalFloorAndCeiling(t *testing.T) {
	d := &ProviderDialer{Discovery: nopFinder{}}
	for _, tc := range []struct {
		env  string
		cfg  time.Duration
		want time.Duration
	}{
		{"", 0, DefaultInterval},
		{"1s", 0, MinInterval},
		{"48h", 0, MaxInterval},
		{"2m", 0, 2 * time.Minute},
		{"garbage-ignored-when-configured", 3 * time.Minute, 3 * time.Minute},
		{"", time.Millisecond, MinInterval},
	} {
		r, err := New(Config{Owner: nopOwner{}, Dialer: d, Interval: tc.cfg, Logger: slog.New(slog.DiscardHandler), Getenv: envMap(map[string]string{EnvSweepInterval: tc.env})})
		if err != nil {
			t.Fatalf("env %q cfg %v: %v", tc.env, tc.cfg, err)
		}
		if r.cfg.Interval != tc.want {
			t.Errorf("env %q cfg %v: interval %v, want %v", tc.env, tc.cfg, r.cfg.Interval, tc.want)
		}
	}
	if _, err := New(Config{Owner: nopOwner{}, Dialer: d, Getenv: envMap(map[string]string{EnvSweepInterval: "soon"})}); err == nil {
		t.Fatal("invalid ERASURE_SWEEP_INTERVAL must fail New")
	}
}

func TestNewRequiresOwnerAndDialer(t *testing.T) {
	if _, err := New(Config{Dialer: &ProviderDialer{Discovery: nopFinder{}}}); err == nil {
		t.Error("missing owner accepted")
	}
	if _, err := New(Config{Owner: nopOwner{}}); err == nil {
		t.Error("missing dialer accepted")
	}
	if _, err := New(Config{Owner: nopOwner{}, Dialer: &ProviderDialer{}}); err == nil {
		t.Error("dialer without discovery accepted")
	}
}

func TestJitterBounds(t *testing.T) {
	base := 5 * time.Minute
	lo, hi := time.Duration(float64(base)*0.8), time.Duration(float64(base)*1.2)
	if got := jittered(base, 0.2, 0); got != lo {
		t.Errorf("r=0: %v want %v", got, lo)
	}
	if got := jittered(base, 0.2, 0.5); got != base {
		t.Errorf("r=0.5: %v want %v", got, base)
	}
	if got := jittered(base, 0.2, 1); got != hi {
		t.Errorf("r=1: %v want %v", got, hi)
	}
	if got := jittered(base, 0, 0.9); got != base {
		t.Errorf("no jitter: %v", got)
	}
	r, err := New(Config{Owner: nopOwner{}, Dialer: &ProviderDialer{Discovery: nopFinder{}}, Interval: base})
	if err != nil {
		t.Fatal(err)
	}
	seenLow, seenHigh := false, false
	for range 20000 {
		d := r.nextDelay(0)
		if d < lo || d > hi {
			t.Fatalf("delay %v outside [%v, %v]", d, lo, hi)
		}
		seenLow = seenLow || d < base-base/10
		seenHigh = seenHigh || d > base+base/10
	}
	if !seenLow || !seenHigh {
		t.Errorf("jitter not spread: low=%v high=%v", seenLow, seenHigh)
	}
	r2, _ := New(Config{Owner: nopOwner{}, Dialer: &ProviderDialer{Discovery: nopFinder{}}, Interval: base, Jitter: 0.9})
	if r2.cfg.Jitter != 0.5 {
		t.Errorf("jitter not capped: %v", r2.cfg.Jitter)
	}
}

func TestRetryBackoffDoublesToInterval(t *testing.T) {
	r, err := New(Config{Owner: nopOwner{}, Dialer: &ProviderDialer{Discovery: nopFinder{}},
		Interval: time.Minute, RetryBackoff: 5 * time.Second, Jitter: -1})
	if err != nil {
		t.Fatal(err)
	}
	want := []time.Duration{time.Minute, 5 * time.Second, 10 * time.Second, 20 * time.Second, 40 * time.Second, time.Minute, time.Minute}
	for failures, w := range want {
		if got := r.nextDelay(failures); got != w {
			t.Errorf("failures=%d: %v want %v", failures, got, w)
		}
	}
	if got := r.nextDelay(1000); got != time.Minute {
		t.Errorf("many failures: %v", got)
	}
}

func TestDialAddr(t *testing.T) {
	for _, tc := range []struct {
		id, adv string
		local   bool
		want    string
	}{
		{"auth-local", ":9400", false, "auth-local:9400"},
		{"auth-local", "0.0.0.0:9400", false, "auth-local:9400"},
		{"auth-local", "[::]:9400", false, "auth-local:9400"},
		{"auth-local", ":9400", true, "127.0.0.1:9400"},
		{"auth-local", "10.0.0.5:9400", true, "10.0.0.5:9400"},
		{"auth-local", "auth-local:9400", false, "auth-local:9400"},
		{"auth-local", "", false, ""},
	} {
		if got := dialAddr(tc.id, tc.adv, tc.local); got != tc.want {
			t.Errorf("dialAddr(%q,%q,%v)=%q want %q", tc.id, tc.adv, tc.local, got, tc.want)
		}
	}
}

func TestTombstoneValidation(t *testing.T) {
	ok := &authv1.UserErasure{ErasureId: "er-1", UserId: "u1", TenantId: "", DeletedAt: "2026-10-09T10:00:00Z"}
	if ts, err := tombstoneFrom(ok); err != nil || ts.UserID != "u1" || ts.DeletedAt.IsZero() {
		t.Fatalf("valid tombstone: %+v %v", ts, err)
	}
	bad := map[string]*authv1.UserErasure{
		"empty user":       {ErasureId: "er-1", UserId: "", DeletedAt: ok.DeletedAt},
		"padded user":      {ErasureId: "er-1", UserId: " u1", DeletedAt: ok.DeletedAt},
		"control in user":  {ErasureId: "er-1", UserId: "u1\n", DeletedAt: ok.DeletedAt},
		"long user":        {ErasureId: "er-1", UserId: strings.Repeat("u", 300), DeletedAt: ok.DeletedAt},
		"invalid utf8":     {ErasureId: "er-1", UserId: "u\xff", DeletedAt: ok.DeletedAt},
		"empty erasure id": {ErasureId: "", UserId: "u1", DeletedAt: ok.DeletedAt},
		"erasure id space": {ErasureId: "er 1", UserId: "u1", DeletedAt: ok.DeletedAt},
		"bad tenant":       {ErasureId: "er-1", UserId: "u1", TenantId: "t\x00", DeletedAt: ok.DeletedAt},
		"bad time":         {ErasureId: "er-1", UserId: "u1", DeletedAt: "yesterday"},
		"no time":          {ErasureId: "er-1", UserId: "u1"},
	}
	for name, e := range bad {
		if _, err := tombstoneFrom(e); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
}

func TestSanitizeCountsAndDetail(t *testing.T) {
	c := Counts{"user_blobs": 3, "Bad Key": 1, "neg": -1, "": 4}
	got := sanitizeCounts(c)
	if len(got) != 1 || got["user_blobs"] != 3 {
		t.Errorf("sanitizeCounts = %v", got)
	}
	big := Counts{}
	for i := range 100 {
		big["k"+strings.Repeat("x", i%40)+string(rune('a'+i%26))] = 1
	}
	if n := len(sanitizeCounts(big)); n > maxCountsEntries {
		t.Errorf("counts not bounded: %d", n)
	}
	if sanitizeCounts(nil) != nil {
		t.Error("nil counts")
	}
	base := errors.New("boom")
	for _, tc := range []struct {
		err  error
		want string
	}{
		{base, DetailApplyFailed},
		{WithDetail("db_locked", base), "db_locked"},
		{WithDetail("User 42 failed", base), DetailApplyFailed},
		{WithDetail(strings.Repeat("a", 65), base), DetailApplyFailed},
	} {
		if got := detailCode(tc.err); got != tc.want {
			t.Errorf("detailCode(%v) = %q want %q", tc.err, got, tc.want)
		}
	}
	if WithDetail("x", nil) != nil {
		t.Error("WithDetail(nil) must be nil")
	}
	if !errors.Is(WithDetail("x", base), base) {
		t.Error("WithDetail must unwrap")
	}
}

func TestRedact(t *testing.T) {
	ts := Tombstone{UserID: "alice-id-123"}
	if got := redact(errors.New("delete alice-id-123: locked (alice-id-123)"), ts); strings.Contains(got, "alice-id-123") {
		t.Errorf("user id not redacted: %q", got)
	}
}
