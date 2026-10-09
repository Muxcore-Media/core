// Package erasuretest provides test doubles for consumers of
// sdk/go/module/erasure: an in-memory identity provider serving the ADR-0035
// ledger RPCs over a real mTLS (or explicit plaintext) gRPC listener, a test
// mesh PKI, a fake core discovery client and a fake Owner.
//
// The fake provider enforces the ADR-0035 §2 caller rules in miniature: with
// TLS, the acknowledging module is the verified client certificate CN and
// must be on Allowed (when set); without TLS it falls back to x-caller-id, as
// the dev profile does.
package erasuretest

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"

	authv1 "github.com/Muxcore-Media/core/proto/gen/muxcore/auth/v1"
	discoveryv1 "github.com/Muxcore-Media/core/proto/gen/muxcore/discovery/v1"
	"github.com/Muxcore-Media/core/sdk/go/module/erasure"
)

// ---------------------------------------------------------------------------
// PKI

// PKI is a throwaway mesh CA for tests.
type PKI struct {
	ca     *x509.Certificate
	caKey  *ecdsa.PrivateKey
	Dir    string
	CAFile string
	serial int64
	mu     sync.Mutex
}

// NewPKI creates a CA in t.TempDir().
func NewPKI(t testing.TB) *PKI {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "erasuretest mesh CA"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	ca, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	p := &PKI{ca: ca, caKey: key, Dir: t.TempDir(), serial: 1}
	p.CAFile = filepath.Join(p.Dir, "ca.crt")
	writePEM(t, p.CAFile, "CERTIFICATE", der)
	return p
}

func writePEM(t testing.TB, path, typ string, der []byte) {
	t.Helper()
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: typ, Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
}

// Issue writes a leaf certificate with CommonName cn, valid as both server
// and client, like core's enrollment. dnsNames default to {cn, "localhost"};
// 127.0.0.1 is always an IP SAN.
func (p *PKI) Issue(t testing.TB, cn string, dnsNames ...string) (certFile, keyFile string) {
	t.Helper()
	p.mu.Lock()
	p.serial++
	serial := p.serial
	p.mu.Unlock()
	if len(dnsNames) == 0 {
		dnsNames = []string{cn, "localhost"}
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(serial), Subject: pkix.Name{CommonName: cn},
		DNSNames: dnsNames, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour),
		KeyUsage:    x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, p.ca, &key.PublicKey, p.caKey)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	name := fmt.Sprintf("leaf-%d", serial)
	certFile = filepath.Join(p.Dir, name+".crt")
	keyFile = filepath.Join(p.Dir, name+".key")
	writePEM(t, certFile, "CERTIFICATE", der)
	writePEM(t, keyFile, "PRIVATE KEY", keyDER)
	return certFile, keyFile
}

// ---------------------------------------------------------------------------
// Fake provider

// Ack is one acknowledgement accepted by the fake provider.
type Ack struct {
	At        time.Time
	Counts    map[string]int64
	ErasureID string
	Module    string
	Detail    string
	Outcome   authv1.ErasureOutcome
}

type ackKey struct{ erasureID, module string }

// Provider is an in-memory identity provider serving the ledger RPCs.
// Methods other than the three ledger RPCs return Unimplemented.
type Provider struct {
	authv1.UnimplementedAuthServiceServer

	latest map[ackKey]Ack
	// Allowed is the CN allowlist (AUTH_ERASURE_CONSUMERS). Nil allows any
	// verified caller.
	Allowed map[string]bool
	// listErr, when non-nil, is returned by ListUserErasures.
	listErr error
	// Required lists AUTH_ERASURE_REQUIRED for GetUserErasureStatus.
	Required []string
	entries  []*authv1.UserErasure
	acks     []Ack
	// ackFailures makes the next n AckUserErasure calls fail with ackCode.
	ackFailures int
	listN       int
	ackN        int
	ackCode     codes.Code
	mu          sync.Mutex
}

// NewProvider returns an empty fake provider.
func NewProvider() *Provider {
	return &Provider{latest: map[ackKey]Ack{}}
}

// AddErasure appends a tombstone and returns its erasure id.
func (p *Provider) AddErasure(userID, tenantID string) string {
	var b [12]byte
	_, _ = rand.Read(b[:])
	id := "er-" + hex.EncodeToString(b[:])
	p.AddRaw(&authv1.UserErasure{
		ErasureId: id, UserId: userID, TenantId: tenantID,
		DeletedAt: time.Now().UTC().Format(time.RFC3339Nano),
	})
	return id
}

// AddRaw appends a tombstone exactly as given (for malformed-input tests).
func (p *Provider) AddRaw(e *authv1.UserErasure) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.entries = append(p.entries, e)
}

// SetListError makes ListUserErasures fail with err (nil clears it).
func (p *Provider) SetListError(err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.listErr = err
}

// FailAcks makes the next n AckUserErasure calls fail with code.
func (p *Provider) FailAcks(n int, code codes.Code) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.ackFailures, p.ackCode = n, code
}

// Acks returns every accepted acknowledgement in order.
func (p *Provider) Acks() []Ack {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]Ack(nil), p.acks...)
}

// Latest returns the most recent acknowledgement of erasureID by module.
func (p *Provider) Latest(erasureID, module string) (Ack, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	a, ok := p.latest[ackKey{erasureID, module}]
	return a, ok
}

// Calls returns the number of ListUserErasures and AckUserErasure calls
// received (including rejected ones).
func (p *Provider) Calls() (list, ack int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.listN, p.ackN
}

// caller identifies the module as ADR-0035 §2 requires: the verified CN with
// TLS; x-caller-id only on a plaintext (dev) connection.
func (p *Provider) caller(ctx context.Context) (string, error) {
	pr, ok := peer.FromContext(ctx)
	if !ok {
		return "", status.Error(codes.Unauthenticated, "no peer")
	}
	var module string
	if ti, ok := pr.AuthInfo.(credentials.TLSInfo); ok {
		if len(ti.State.VerifiedChains) == 0 || len(ti.State.VerifiedChains[0]) == 0 {
			return "", status.Error(codes.Unauthenticated, "verified client certificate required")
		}
		module = ti.State.VerifiedChains[0][0].Subject.CommonName
	} else {
		md, _ := metadata.FromIncomingContext(ctx)
		if v := md.Get("x-caller-id"); len(v) == 1 {
			module = v[0]
		}
	}
	if module == "" {
		return "", status.Error(codes.Unauthenticated, "module identity required")
	}
	if p.Allowed != nil && !p.Allowed[module] {
		return "", status.Errorf(codes.PermissionDenied, "module %q may not read the erasure ledger", module)
	}
	return module, nil
}

func pageBounds(size int32, token string, n int) (start, end int, err error) {
	switch {
	case size == 0:
		size = 100
	case size < 0 || size > 500:
		return 0, 0, status.Error(codes.InvalidArgument, "page_size out of range")
	}
	if token != "" {
		raw, derr := base64.RawURLEncoding.DecodeString(token)
		if derr != nil {
			return 0, 0, status.Error(codes.InvalidArgument, "malformed page_token")
		}
		start, err = strconv.Atoi(string(raw))
		if err != nil || start < 0 || start > n {
			return 0, 0, status.Error(codes.InvalidArgument, "malformed page_token")
		}
	}
	return start, min(start+int(size), n), nil
}

func pageToken(end, n int) string {
	if end >= n {
		return ""
	}
	return base64.RawURLEncoding.EncodeToString([]byte(strconv.Itoa(end)))
}

// ListUserErasures pages through the ledger in insertion order.
func (p *Provider) ListUserErasures(ctx context.Context, req *authv1.ListUserErasuresRequest) (*authv1.ListUserErasuresResponse, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.listN++
	module, err := p.caller(ctx)
	if err != nil {
		return nil, err
	}
	if p.listErr != nil {
		return nil, p.listErr
	}
	start, end, err := pageBounds(req.GetPageSize(), req.GetPageToken(), len(p.entries))
	if err != nil {
		return nil, err
	}
	resp := &authv1.ListUserErasuresResponse{NextPageToken: pageToken(end, len(p.entries))}
	for _, e := range p.entries[start:end] {
		c := cloneErasure(e)
		a, ok := p.latest[ackKey{e.GetErasureId(), module}]
		c.AcknowledgedByCaller = ok && a.Outcome == authv1.ErasureOutcome_ERASURE_OUTCOME_OK
		resp.Erasures = append(resp.Erasures, c)
	}
	return resp, nil
}

func cloneErasure(e *authv1.UserErasure) *authv1.UserErasure {
	return &authv1.UserErasure{
		ErasureId: e.GetErasureId(), UserId: e.GetUserId(), TenantId: e.GetTenantId(), DeletedAt: e.GetDeletedAt(),
	}
}

// AckUserErasure records the acknowledgement under the caller's verified
// identity. Any module field a client might try to send does not exist.
func (p *Provider) AckUserErasure(ctx context.Context, req *authv1.AckUserErasureRequest) (*authv1.AckUserErasureResponse, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.ackN++
	module, err := p.caller(ctx)
	if err != nil {
		return nil, err
	}
	if p.ackFailures > 0 {
		p.ackFailures--
		return nil, status.Error(p.ackCode, "injected acknowledgement failure")
	}
	if req.GetOutcome() == authv1.ErasureOutcome_ERASURE_OUTCOME_UNSPECIFIED {
		return nil, status.Error(codes.InvalidArgument, "outcome required")
	}
	if req.GetDetailCode() != "" && !erasure.ValidDetailCode(req.GetDetailCode()) {
		return nil, status.Error(codes.InvalidArgument, "invalid detail_code")
	}
	if len(req.GetCounts()) > 32 {
		return nil, status.Error(codes.InvalidArgument, "too many counts")
	}
	for k, v := range req.GetCounts() {
		if !erasure.ValidDetailCode(k) || v < 0 {
			return nil, status.Error(codes.InvalidArgument, "invalid counts")
		}
	}
	known := false
	for _, e := range p.entries {
		if e.GetErasureId() == req.GetErasureId() {
			known = true
			break
		}
	}
	if !known {
		return nil, status.Error(codes.NotFound, "unknown erasure")
	}
	a := Ack{At: time.Now().UTC(), ErasureID: req.GetErasureId(), Module: module, Outcome: req.GetOutcome(), Detail: req.GetDetailCode(), Counts: req.GetCounts()}
	p.acks = append(p.acks, a)
	p.latest[ackKey{a.ErasureID, module}] = a
	return &authv1.AckUserErasureResponse{}, nil
}

// GetUserErasureStatus reports completion against Required. The fake does
// not check the admin bearer; it exists for status rendering tests.
func (p *Provider) GetUserErasureStatus(_ context.Context, req *authv1.GetUserErasureStatusRequest) (*authv1.GetUserErasureStatusResponse, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	var all []*authv1.ErasureStatus
	for _, e := range p.entries {
		if req.GetErasureId() != "" && e.GetErasureId() != req.GetErasureId() {
			continue
		}
		st := p.statusLocked(e)
		if req.GetErasureId() == "" && req.GetPendingOnly() && st.GetComplete() {
			continue
		}
		all = append(all, st)
	}
	if req.GetErasureId() != "" {
		if len(all) == 0 {
			return nil, status.Error(codes.NotFound, "unknown erasure")
		}
		return &authv1.GetUserErasureStatusResponse{Erasures: all}, nil
	}
	start, end, err := pageBounds(req.GetPageSize(), req.GetPageToken(), len(all))
	if err != nil {
		return nil, err
	}
	return &authv1.GetUserErasureStatusResponse{Erasures: all[start:end], NextPageToken: pageToken(end, len(all))}, nil
}

func (p *Provider) statusLocked(e *authv1.UserErasure) *authv1.ErasureStatus {
	st := &authv1.ErasureStatus{ErasureId: e.GetErasureId(), DeletedAt: e.GetDeletedAt(), Complete: true}
	required := map[string]bool{}
	for _, m := range p.Required {
		required[m] = true
		a, ok := p.latest[ackKey{e.GetErasureId(), m}]
		ms := &authv1.ErasureModuleStatus{ModuleId: m, Required: true}
		if ok {
			ms.Outcome, ms.DetailCode = a.Outcome, a.Detail
			ms.AckedAt = a.At.Format(time.RFC3339)
		}
		if ms.GetOutcome() != authv1.ErasureOutcome_ERASURE_OUTCOME_OK {
			st.Complete = false
		}
		st.Modules = append(st.Modules, ms)
	}
	var others []string
	for k := range p.latest {
		if k.erasureID == e.GetErasureId() && !required[k.module] {
			others = append(others, k.module)
		}
	}
	sort.Strings(others)
	for _, m := range others {
		a := p.latest[ackKey{e.GetErasureId(), m}]
		st.Modules = append(st.Modules, &authv1.ErasureModuleStatus{ModuleId: m, Outcome: a.Outcome, DetailCode: a.Detail, AckedAt: a.At.Format(time.RFC3339)})
	}
	return st
}

// ---------------------------------------------------------------------------
// Serving

// ServeTLS serves srv on 127.0.0.1 with the given server certificate. Client
// certificates are requested and verified against caFile when presented,
// like a mesh sidecar (meshtls.ServerOption); the Provider decides whether a
// caller without one is acceptable. It returns the listen address.
func ServeTLS(t testing.TB, srv authv1.AuthServiceServer, certFile, keyFile, caFile string) string {
	t.Helper()
	pair, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		t.Fatal(err)
	}
	caPEM, err := os.ReadFile(caFile) //nolint:gosec // test fixture path
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM(caPEM)
	cfg := &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{pair}, ClientCAs: pool, ClientAuth: tls.VerifyClientCertIfGiven}
	return serve(t, srv, grpc.Creds(credentials.NewTLS(cfg)))
}

// ServePlain serves srv without TLS (dev profile fixtures only).
func ServePlain(t testing.TB, srv authv1.AuthServiceServer) string {
	t.Helper()
	return serve(t, srv)
}

func serve(t testing.TB, srv authv1.AuthServiceServer, opts ...grpc.ServerOption) string {
	t.Helper()
	var lc net.ListenConfig
	lis, err := lc.Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	gs := grpc.NewServer(opts...)
	authv1.RegisterAuthServiceServer(gs, srv)
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = gs.Serve(lis)
	}()
	t.Cleanup(func() {
		gs.Stop()
		<-done
	})
	return lis.Addr().String()
}

// ---------------------------------------------------------------------------
// Discovery

// Discovery is a fake core DiscoveryService answering FindByCapability.
type Discovery struct {
	// Err, when set, is returned by FindByCapability.
	Err     error
	modules map[string][]*discoveryv1.ModuleInfoProto
	calls   map[string]int
	mu      sync.Mutex
}

// NewDiscovery returns a discovery client that reports the given modules
// for the "identity" capability.
func NewDiscovery(identity ...*discoveryv1.ModuleInfoProto) *Discovery {
	return &Discovery{modules: map[string][]*discoveryv1.ModuleInfoProto{"identity": identity}, calls: map[string]int{}}
}

// Module builds a discovery entry.
func Module(id, addr string) *discoveryv1.ModuleInfoProto {
	return &discoveryv1.ModuleInfoProto{Id: id, HttpAddr: addr, Capabilities: []string{"identity"}, State: "running"}
}

// Set replaces the modules reported for capability.
func (d *Discovery) Set(capability string, modules ...*discoveryv1.ModuleInfoProto) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.modules[capability] = modules
}

// Calls returns how often capability was looked up.
func (d *Discovery) Calls(capability string) int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.calls[capability]
}

// FindByCapability implements erasure.CapabilityFinder.
func (d *Discovery) FindByCapability(_ context.Context, in *discoveryv1.FindByCapabilityRequest, _ ...grpc.CallOption) (*discoveryv1.FindByCapabilityResponse, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.calls[in.GetCapability()]++
	if d.Err != nil {
		return nil, d.Err
	}
	return &discoveryv1.FindByCapabilityResponse{Modules: d.modules[in.GetCapability()]}, nil
}

// ---------------------------------------------------------------------------
// Owner

// Owner is an in-memory personal-data store. Rows maps user id to the number
// of rows the user owns; Apply deletes them and records the erasure in one
// critical section, standing in for one local transaction.
type Owner struct {
	// FailApply, when set, is consulted before Apply changes anything; a
	// non-nil error rolls back (nothing changes).
	FailApply func(erasure.Tombstone) error
	// FailApplied, when set, makes Applied fail.
	FailApplied func(erasureID string) error
	// Leak, when set, makes Apply leave this many rows behind (post-condition
	// failure) while still recording the erasure.
	Leak       func(erasure.Tombstone) int
	rows       map[string]int
	applied    map[string]bool
	applyCalls map[string]int
	ID         string
	// ApplyDelay is slept at the start of Apply, before the critical
	// section, so overlapping calls are observable (MaxConcurrentApply).
	ApplyDelay time.Duration
	active     atomic.Int32
	maxActive  atomic.Int32
	mu         sync.Mutex
}

// NewOwner returns an owner for module id with the given rows per user.
func NewOwner(id string, rows map[string]int) *Owner {
	r := map[string]int{}
	for k, v := range rows {
		r[k] = v
	}
	return &Owner{ID: id, rows: r, applied: map[string]bool{}, applyCalls: map[string]int{}}
}

// ModuleID implements erasure.Owner.
func (o *Owner) ModuleID() string { return o.ID }

// Applied implements erasure.Owner.
func (o *Owner) Applied(_ context.Context, erasureID string) (bool, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.FailApplied != nil {
		if err := o.FailApplied(erasureID); err != nil {
			return false, err
		}
	}
	return o.applied[erasureID], nil
}

// Apply implements erasure.Owner.
func (o *Owner) Apply(_ context.Context, t erasure.Tombstone) (erasure.Counts, error) {
	n := o.active.Add(1)
	defer o.active.Add(-1)
	for {
		m := o.maxActive.Load()
		if n <= m || o.maxActive.CompareAndSwap(m, n) {
			break
		}
	}
	if o.ApplyDelay > 0 {
		time.Sleep(o.ApplyDelay)
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	o.applyCalls[t.ErasureID]++
	if o.FailApply != nil {
		if err := o.FailApply(t); err != nil {
			return nil, err
		}
	}
	had := o.rows[t.UserID]
	left := 0
	if o.Leak != nil {
		left = o.Leak(t)
	}
	o.rows[t.UserID] = left
	if left == 0 {
		delete(o.rows, t.UserID)
	}
	o.applied[t.ErasureID] = true
	return erasure.Counts{"rows": int64(had - left)}, nil
}

// Verify implements erasure.Verifier: rows still owned by the user.
func (o *Owner) Verify(_ context.Context, t erasure.Tombstone) (int, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.rows[t.UserID], nil
}

// Rows returns the rows left for userID.
func (o *Owner) Rows(userID string) int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.rows[userID]
}

// ApplyCalls returns how often Apply ran for erasureID.
func (o *Owner) ApplyCalls(erasureID string) int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.applyCalls[erasureID]
}

// TotalApplyCalls returns how often Apply ran in total.
func (o *Owner) TotalApplyCalls() int {
	o.mu.Lock()
	defer o.mu.Unlock()
	n := 0
	for _, c := range o.applyCalls {
		n += c
	}
	return n
}

// MarkApplied records erasureID as applied without erasing (restored or
// pre-existing local state).
func (o *Owner) MarkApplied(erasureID string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.applied[erasureID] = true
}

// MaxConcurrentApply is the largest number of overlapping Apply calls seen.
func (o *Owner) MaxConcurrentApply() int { return int(o.maxActive.Load()) }

// IsApplied reports the local applied record.
func (o *Owner) IsApplied(erasureID string) bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.applied[erasureID]
}
