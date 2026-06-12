package grpcmesh

import (
	"context"
	"errors"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/Muxcore-Media/core/internal/callerid"
	"github.com/Muxcore-Media/core/internal/config"
	"github.com/Muxcore-Media/core/internal/registry"
	"github.com/Muxcore-Media/core/pkg/contracts"
	auditv1 "github.com/Muxcore-Media/core/proto/gen/muxcore/audit/v1"
	lifecyclev1 "github.com/Muxcore-Media/core/proto/gen/muxcore/lifecycle/v1"
	spoolv1 "github.com/Muxcore-Media/core/proto/gen/muxcore/spool/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type fullStubAuditLogger struct {
	queryFn       func(ctx context.Context, filter contracts.AuditFilter) ([]contracts.AuditEntry, error)
	verifyChainFn func(ctx context.Context, from, to time.Time) (contracts.ChainVerificationResult, error)
}

func (l *fullStubAuditLogger) Log(_ context.Context, _ contracts.AuditEntry) error {
	return nil
}

func (l *fullStubAuditLogger) Query(ctx context.Context, filter contracts.AuditFilter) ([]contracts.AuditEntry, error) {
	if l.queryFn != nil {
		return l.queryFn(ctx, filter)
	}
	return nil, nil
}

func (l *fullStubAuditLogger) Export(_ context.Context, _ string) (io.ReadCloser, error) {
	return nil, nil
}

func (l *fullStubAuditLogger) VerifyChainIntegrity(ctx context.Context, from, to time.Time) (contracts.ChainVerificationResult, error) {
	if l.verifyChainFn != nil {
		return l.verifyChainFn(ctx, from, to)
	}
	return contracts.ChainVerificationResult{Valid: true}, nil
}

func (l *fullStubAuditLogger) VerifyAll(_ context.Context) (contracts.ChainVerificationResult, error) {
	return contracts.ChainVerificationResult{Valid: true}, nil
}

// --- AuditServer Tests ---

func TestAuditServer_NewWithNilLogger(t *testing.T) {
	srv := NewAuditServer(nil)
	if srv == nil {
		t.Fatal("expected non-nil server")
	}
	if srv.logger != nil {
		t.Error("expected nil logger")
	}
}

func TestAuditServer_CheckAuth(t *testing.T) {
	srv := NewAuditServer(nil)

	tests := []struct {
		name   string
		caller string
		setID  bool
		want   codes.Code
	}{
		{"empty caller", "", false, codes.PermissionDenied},
		{"public caller", "_public", true, codes.PermissionDenied},
		{"valid caller", "admin:test", true, codes.OK},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			if tc.setID {
				ctx = callerid.Set(ctx, tc.caller)
			}
			err := srv.checkAuth(ctx)
			if tc.want == codes.OK {
				if err != nil {
					t.Fatalf("expected no error, got %v", err)
				}
			} else {
				if err == nil {
					t.Fatal("expected error")
				}
				if status.Code(err) != tc.want {
					t.Errorf("expected %s, got %s", tc.want, status.Code(err))
				}
			}
		})
	}
}

func TestAuditServer_Query_NilLogger(t *testing.T) {
	srv := NewAuditServer(nil)
	ctx := callerid.Set(context.Background(), "admin:test")

	resp, err := srv.Query(ctx, &auditv1.AuditQueryRequest{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(resp.Entries) != 0 {
		t.Errorf("expected 0 entries, got %d", len(resp.Entries))
	}
	if resp.Total != 0 {
		t.Errorf("expected total 0, got %d", resp.Total)
	}
}

func TestAuditServer_Query_CallsLogger(t *testing.T) {
	ts := time.Date(2025, 1, 15, 10, 0, 0, 0, time.UTC)
	logger := &fullStubAuditLogger{
		queryFn: func(_ context.Context, filter contracts.AuditFilter) ([]contracts.AuditEntry, error) {
			if filter.Actor != "user-1" {
				t.Errorf("expected actor filter 'user-1', got %q", filter.Actor)
			}
			return []contracts.AuditEntry{
				{
					ID:        "entry-1",
					Timestamp: ts,
					Actor:     "user-1",
					Action:    "http.request",
					Resource:  "/api/test",
				},
				{
					ID:        "entry-2",
					Timestamp: ts,
					Actor:     "user-1",
					Action:    "module.start",
					Resource:  "mod-a",
				},
			}, nil
		},
	}

	srv := NewAuditServer(logger)
	ctx := callerid.Set(context.Background(), "admin:test")

	resp, err := srv.Query(ctx, &auditv1.AuditQueryRequest{
		Actor: "user-1",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Total != 2 {
		t.Errorf("expected total 2, got %d", resp.Total)
	}
	if len(resp.Entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(resp.Entries))
	}
	if resp.Entries[0].Id != "entry-1" {
		t.Errorf("expected entry-1, got %s", resp.Entries[0].Id)
	}
	if resp.Entries[1].Action != "module.start" {
		t.Errorf("expected module.start, got %s", resp.Entries[1].Action)
	}
}

func TestAuditServer_Query_LoggerError(t *testing.T) {
	logger := &fullStubAuditLogger{
		queryFn: func(_ context.Context, _ contracts.AuditFilter) ([]contracts.AuditEntry, error) {
			return nil, errors.New("db down")
		},
	}
	srv := NewAuditServer(logger)
	ctx := callerid.Set(context.Background(), "admin:test")

	_, err := srv.Query(ctx, &auditv1.AuditQueryRequest{})
	if err == nil {
		t.Fatal("expected error")
	}
	if status.Code(err) != codes.Unavailable {
		t.Errorf("expected Unavailable, got %s", status.Code(err))
	}
}

func TestAuditServer_VerifyChain_NilLogger(t *testing.T) {
	srv := NewAuditServer(nil)
	ctx := callerid.Set(context.Background(), "admin:test")

	resp, err := srv.VerifyChain(ctx, &auditv1.AuditVerifyChainRequest{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !resp.Valid {
		t.Error("expected valid=true when logger is nil")
	}
}

func TestAuditServer_VerifyChain_InvalidTimeFormat(t *testing.T) {
	srv := NewAuditServer(&fullStubAuditLogger{})
	ctx := callerid.Set(context.Background(), "admin:test")

	_, err := srv.VerifyChain(ctx, &auditv1.AuditVerifyChainRequest{
		FromTime: "not-a-time",
	})
	if err == nil {
		t.Fatal("expected error for invalid time format")
	}
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("expected InvalidArgument, got %s", status.Code(err))
	}
}

func TestAuditServer_VerifyChain_InvalidToTime(t *testing.T) {
	srv := NewAuditServer(&fullStubAuditLogger{})
	ctx := callerid.Set(context.Background(), "admin:test")

	_, err := srv.VerifyChain(ctx, &auditv1.AuditVerifyChainRequest{
		ToTime: "bad-time",
	})
	if err == nil {
		t.Fatal("expected error for invalid to_time format")
	}
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("expected InvalidArgument, got %s", status.Code(err))
	}
}

func TestAuditServer_VerifyChain_CallsLogger(t *testing.T) {
	brokenTime := time.Date(2025, 3, 1, 12, 0, 0, 0, time.UTC)
	logger := &fullStubAuditLogger{
		verifyChainFn: func(_ context.Context, _, _ time.Time) (contracts.ChainVerificationResult, error) {
			return contracts.ChainVerificationResult{
				Valid:         false,
				TotalEntries:  100,
				BrokenLinks:   []string{"entry-42"},
				FirstBrokenAt: brokenTime,
			}, nil
		},
	}
	srv := NewAuditServer(logger)
	ctx := callerid.Set(context.Background(), "admin:test")

	resp, err := srv.VerifyChain(ctx, &auditv1.AuditVerifyChainRequest{
		FromTime: "2025-01-01T00:00:00Z",
		ToTime:   "2025-06-01T00:00:00Z",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Valid {
		t.Error("expected valid=false")
	}
	if resp.TotalEntries != 100 {
		t.Errorf("expected 100 total entries, got %d", resp.TotalEntries)
	}
	if len(resp.BrokenLinks) != 1 || resp.BrokenLinks[0] != "entry-42" {
		t.Errorf("unexpected broken links: %v", resp.BrokenLinks)
	}
	if resp.FirstBrokenAt != brokenTime.Format(time.RFC3339) {
		t.Errorf("expected %s, got %s", brokenTime.Format(time.RFC3339), resp.FirstBrokenAt)
	}
}

func TestAuditFilterFromProto(t *testing.T) {
	fromStr := "2025-01-01T00:00:00Z"
	toStr := "2025-06-01T00:00:00Z"
	req := &auditv1.AuditQueryRequest{
		Actor:       "user-1",
		Action:      "http.request",
		Resource:    "/api/test",
		TraceId:     "trace-abc",
		FromTime:    fromStr,
		ToTime:      toStr,
		VerifyChain: true,
	}

	filter := auditFilterFromProto(req)

	if filter.Actor != "user-1" {
		t.Errorf("expected actor 'user-1', got %q", filter.Actor)
	}
	if filter.Action != "http.request" {
		t.Errorf("expected action 'http.request', got %q", filter.Action)
	}
	if filter.Resource != "/api/test" {
		t.Errorf("expected resource '/api/test', got %q", filter.Resource)
	}
	if filter.TraceID != "trace-abc" {
		t.Errorf("expected traceID 'trace-abc', got %q", filter.TraceID)
	}
	if !filter.VerifyChain {
		t.Error("expected verifyChain=true")
	}

	expectedFrom, _ := time.Parse(time.RFC3339, fromStr)
	if !filter.From.Equal(expectedFrom) {
		t.Errorf("expected from %v, got %v", expectedFrom, filter.From)
	}
	expectedTo, _ := time.Parse(time.RFC3339, toStr)
	if !filter.To.Equal(expectedTo) {
		t.Errorf("expected to %v, got %v", expectedTo, filter.To)
	}
}

func TestAuditFilterFromProto_EmptyTimes(t *testing.T) {
	req := &auditv1.AuditQueryRequest{
		Actor: "sys",
	}
	filter := auditFilterFromProto(req)
	if filter.Actor != "sys" {
		t.Errorf("expected actor 'sys', got %q", filter.Actor)
	}
	if !filter.From.IsZero() {
		t.Errorf("expected zero from time, got %v", filter.From)
	}
	if !filter.To.IsZero() {
		t.Errorf("expected zero to time, got %v", filter.To)
	}
}

func TestAuditFilterFromProto_InvalidTimeIgnored(t *testing.T) {
	req := &auditv1.AuditQueryRequest{
		FromTime: "not-valid",
		ToTime:   "also-not-valid",
	}
	filter := auditFilterFromProto(req)
	if !filter.From.IsZero() {
		t.Errorf("expected zero from time for invalid input, got %v", filter.From)
	}
	if !filter.To.IsZero() {
		t.Errorf("expected zero to time for invalid input, got %v", filter.To)
	}
}

func TestAuditEntryToProto(t *testing.T) {
	ts := time.Date(2025, 6, 15, 12, 30, 0, 0, time.UTC)
	entry := contracts.AuditEntry{
		ID:            "abc-123",
		Timestamp:     ts,
		Actor:         "admin",
		Action:        "module.registered",
		Resource:      "mod-x",
		ResourceID:    "uuid-1",
		Details:       map[string]string{"key": "val"},
		TraceID:       "trace-99",
		NodeID:        "node-3",
		PrevEntryHash: "deadbeef",
		Signature:     "sig-abc",
	}

	p := auditEntryToProto(entry)

	if p.Id != "abc-123" {
		t.Errorf("expected abc-123, got %s", p.Id)
	}
	if p.Timestamp != ts.Unix() {
		t.Errorf("expected %d, got %d", ts.Unix(), p.Timestamp)
	}
	if p.Actor != "admin" {
		t.Errorf("expected admin, got %s", p.Actor)
	}
	if p.Action != "module.registered" {
		t.Errorf("expected module.registered, got %s", p.Action)
	}
	if p.Resource != "mod-x" {
		t.Errorf("expected mod-x, got %s", p.Resource)
	}
	if p.ResourceId != "uuid-1" {
		t.Errorf("expected uuid-1, got %s", p.ResourceId)
	}
	if p.Details["key"] != "val" {
		t.Errorf("expected details key=val, got %v", p.Details)
	}
	if p.TraceId != "trace-99" {
		t.Errorf("expected trace-99, got %s", p.TraceId)
	}
	if p.NodeId != "node-3" {
		t.Errorf("expected node-3, got %s", p.NodeId)
	}
	if p.PrevEntryHash != "deadbeef" {
		t.Errorf("expected deadbeef, got %s", p.PrevEntryHash)
	}
	if p.Signature != "sig-abc" {
		t.Errorf("expected sig-abc, got %s", p.Signature)
	}
}

// --- LifecycleServer Tests ---

func TestLifecycleServer_New(t *testing.T) {
	reg := registry.New()
	srv := NewLifecycleServer(reg, nil, "node-1", nil)
	if srv == nil {
		t.Fatal("expected non-nil server")
	}
	if srv.nodeID != "node-1" {
		t.Errorf("expected node-1, got %s", srv.nodeID)
	}
}

func TestLifecycleServer_CheckAuth(t *testing.T) {
	srv := NewLifecycleServer(registry.New(), nil, "node-1", nil)

	tests := []struct {
		name   string
		caller string
		setID  bool
		want   codes.Code
	}{
		{"empty caller", "", false, codes.PermissionDenied},
		{"public caller", "_public", true, codes.PermissionDenied},
		{"valid caller", "admin:node", true, codes.OK},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			if tc.setID {
				ctx = callerid.Set(ctx, tc.caller)
			}
			err := srv.checkAuth(ctx)
			if tc.want == codes.OK {
				if err != nil {
					t.Fatalf("expected no error, got %v", err)
				}
			} else {
				if err == nil {
					t.Fatal("expected error")
				}
				if status.Code(err) != tc.want {
					t.Errorf("expected %s, got %s", tc.want, status.Code(err))
				}
			}
		})
	}
}

func TestLifecycleServer_ListModules_StateFilter(t *testing.T) {
	reg := registry.New()
	srv := NewLifecycleServer(reg, nil, "node-1", nil)

	registerTestModule(t, reg, "mod-a", "provider", []string{"storage"})
	registerTestModule(t, reg, "mod-b", "worker", []string{"compute"})

	ctx := callerid.Set(context.Background(), "admin:test")

	resp, err := srv.ListModules(ctx, &lifecyclev1.ListModulesRequest{
		StateFilter: string(contracts.ModuleStateRegistered),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Total != 2 {
		t.Errorf("expected 2 modules (both registered), got %d", resp.Total)
	}

	resp, err = srv.ListModules(ctx, &lifecyclev1.ListModulesRequest{
		StateFilter: "running",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Total != 0 {
		t.Errorf("expected 0 running modules, got %d", resp.Total)
	}
}

func TestLifecycleServer_ListModules_CapabilityFilter(t *testing.T) {
	reg := registry.New()
	srv := NewLifecycleServer(reg, nil, "node-1", nil)

	registerTestModule(t, reg, "mod-a", "provider", []string{"storage.local", "cache"})
	registerTestModule(t, reg, "mod-b", "worker", []string{"compute"})

	ctx := callerid.Set(context.Background(), "admin:test")

	resp, err := srv.ListModules(ctx, &lifecyclev1.ListModulesRequest{
		CapabilityFilter: "storage.local",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Total != 1 {
		t.Fatalf("expected 1 module, got %d", resp.Total)
	}
	if resp.Modules[0].ModuleId != "mod-a" {
		t.Errorf("expected mod-a, got %s", resp.Modules[0].ModuleId)
	}
	if resp.Modules[0].NodeId != "node-1" {
		t.Errorf("expected node-1, got %s", resp.Modules[0].NodeId)
	}
}

func TestLifecycleServer_ListModules_NoFilter(t *testing.T) {
	reg := registry.New()
	srv := NewLifecycleServer(reg, nil, "node-1", nil)

	registerTestModule(t, reg, "mod-a", "provider", nil)
	registerTestModule(t, reg, "mod-b", "worker", nil)

	ctx := callerid.Set(context.Background(), "admin:test")

	resp, err := srv.ListModules(ctx, &lifecyclev1.ListModulesRequest{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Total != 2 {
		t.Errorf("expected 2 modules, got %d", resp.Total)
	}
}

func TestLifecycleServer_StopModule_EmptyID(t *testing.T) {
	srv := NewLifecycleServer(registry.New(), nil, "node-1", nil)
	ctx := callerid.Set(context.Background(), "admin:test")

	_, err := srv.StopModule(ctx, &lifecyclev1.StopModuleRequest{ModuleId: ""})
	if err == nil {
		t.Fatal("expected error for empty module_id")
	}
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("expected InvalidArgument, got %s", status.Code(err))
	}
}

func TestLifecycleServer_StopModule_UnknownModule(t *testing.T) {
	srv := NewLifecycleServer(registry.New(), nil, "node-1", nil)
	ctx := callerid.Set(context.Background(), "admin:test")

	resp, err := srv.StopModule(ctx, &lifecyclev1.StopModuleRequest{ModuleId: "nonexistent"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Acknowledged {
		t.Error("expected acknowledged=false for unknown module")
	}
	if resp.Error == "" {
		t.Error("expected error message for unknown module")
	}
}

func TestLifecycleServer_StopModule_KnownModule(t *testing.T) {
	reg := registry.New()
	srv := NewLifecycleServer(reg, nil, "node-1", nil)
	registerTestModule(t, reg, "mod-a", "provider", nil)

	ctx := callerid.Set(context.Background(), "admin:test")

	resp, err := srv.StopModule(ctx, &lifecyclev1.StopModuleRequest{ModuleId: "mod-a"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !resp.Acknowledged {
		t.Error("expected acknowledged=true")
	}
}

func TestLifecycleServer_RestartModule_EmptyID(t *testing.T) {
	srv := NewLifecycleServer(registry.New(), nil, "node-1", nil)
	ctx := callerid.Set(context.Background(), "admin:test")

	_, err := srv.RestartModule(ctx, &lifecyclev1.RestartModuleRequest{ModuleId: ""})
	if err == nil {
		t.Fatal("expected error for empty module_id")
	}
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("expected InvalidArgument, got %s", status.Code(err))
	}
}

// --- SpoolServer Tests ---

func TestSpoolServer_New(t *testing.T) {
	reg := registry.New()
	cfg := &config.Config{Spool: config.SpoolConfig{AllowedHosts: []string{"github.com"}}}
	srv := NewSpoolServer("https://spool.example.com", cfg, &sync.Mutex{}, nil, reg)
	if srv == nil {
		t.Fatal("expected non-nil server")
	}
	if srv.spoolURL != "https://spool.example.com" {
		t.Errorf("expected spool URL, got %s", srv.spoolURL)
	}
}

func TestSpoolServer_CheckAuth(t *testing.T) {
	srv := NewSpoolServer("https://spool.example.com", &config.Config{}, &sync.Mutex{}, nil, registry.New())

	tests := []struct {
		name   string
		caller string
		setID  bool
		want   codes.Code
	}{
		{"empty caller", "", false, codes.PermissionDenied},
		{"public caller", "_public", true, codes.PermissionDenied},
		{"valid caller", "admin:test", true, codes.OK},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			if tc.setID {
				ctx = callerid.Set(ctx, tc.caller)
			}
			err := srv.checkAuth(ctx)
			if tc.want == codes.OK {
				if err != nil {
					t.Fatalf("expected no error, got %v", err)
				}
			} else {
				if err == nil {
					t.Fatal("expected error")
				}
				if status.Code(err) != tc.want {
					t.Errorf("expected %s, got %s", tc.want, status.Code(err))
				}
			}
		})
	}
}

func TestSpoolServer_ListSpools(t *testing.T) {
	cfg := &config.Config{
		Spool: config.SpoolConfig{AllowedHosts: []string{"github.com", "gitlab.com"}},
	}
	srv := NewSpoolServer("https://spool.example.com", cfg, &sync.Mutex{}, nil, registry.New())
	ctx := callerid.Set(context.Background(), "admin:test")

	resp, err := srv.ListSpools(ctx, &spoolv1.ListSpoolsRequest{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(resp.Spools) != 1 {
		t.Fatalf("expected 1 spool, got %d", len(resp.Spools))
	}
	s := resp.Spools[0]
	if s.Url != "https://spool.example.com" {
		t.Errorf("expected spool URL, got %s", s.Url)
	}
	if !s.Active {
		t.Error("expected active=true")
	}
	if len(s.AllowedHosts) != 2 {
		t.Errorf("expected 2 allowed hosts, got %d", len(s.AllowedHosts))
	}
	if s.AllowedHosts[0] != "github.com" {
		t.Errorf("expected github.com, got %s", s.AllowedHosts[0])
	}
}

func TestSpoolServer_ListSpools_EmptyHosts(t *testing.T) {
	cfg := &config.Config{}
	srv := NewSpoolServer("https://spool.test", cfg, &sync.Mutex{}, nil, registry.New())
	ctx := callerid.Set(context.Background(), "admin:test")

	resp, err := srv.ListSpools(ctx, &spoolv1.ListSpoolsRequest{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(resp.Spools) != 1 {
		t.Fatalf("expected 1 spool, got %d", len(resp.Spools))
	}
	if len(resp.Spools[0].AllowedHosts) != 0 {
		t.Errorf("expected 0 allowed hosts, got %d", len(resp.Spools[0].AllowedHosts))
	}
}

func TestSpoolServer_FetchTag_EmptyTagName(t *testing.T) {
	srv := NewSpoolServer("https://spool.test", &config.Config{}, &sync.Mutex{}, nil, registry.New())
	ctx := callerid.Set(context.Background(), "admin:test")

	_, err := srv.FetchTag(ctx, &spoolv1.FetchTagRequest{TagName: ""})
	if err == nil {
		t.Fatal("expected error for empty tag_name")
	}
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("expected InvalidArgument, got %s", status.Code(err))
	}
}

func TestSpoolServer_FetchTag_AuthRequired(t *testing.T) {
	srv := NewSpoolServer("https://spool.test", &config.Config{}, &sync.Mutex{}, nil, registry.New())
	ctx := context.Background()

	_, err := srv.FetchTag(ctx, &spoolv1.FetchTagRequest{TagName: "v1.0"})
	if err == nil {
		t.Fatal("expected auth error")
	}
	if status.Code(err) != codes.PermissionDenied {
		t.Errorf("expected PermissionDenied, got %s", status.Code(err))
	}
}

func TestSpoolServer_FetchTag_DefaultSpoolURL(t *testing.T) {
	srv := NewSpoolServer("https://spool.test", &config.Config{}, &sync.Mutex{}, nil, registry.New())
	ctx := callerid.Set(context.Background(), "admin:test")

	_, err := srv.FetchTag(ctx, &spoolv1.FetchTagRequest{TagName: "v1.0"})
	if err == nil {
		t.Fatal("expected error from unreachable spool")
	}
	if status.Code(err) != codes.Unavailable {
		t.Errorf("expected Unavailable, got %s", status.Code(err))
	}
}

func TestSpoolServer_ListTags_AuthRequired(t *testing.T) {
	srv := NewSpoolServer("https://spool.test", &config.Config{}, &sync.Mutex{}, nil, registry.New())
	ctx := context.Background()

	_, err := srv.ListTags(ctx, &spoolv1.ListTagsRequest{})
	if err == nil {
		t.Fatal("expected auth error")
	}
	if status.Code(err) != codes.PermissionDenied {
		t.Errorf("expected PermissionDenied, got %s", status.Code(err))
	}
}

func TestSpoolServer_ListTags_ReturnsEmptyOnFetchError(t *testing.T) {
	srv := NewSpoolServer("https://spool.test", &config.Config{}, &sync.Mutex{}, nil, registry.New())
	ctx := callerid.Set(context.Background(), "admin:test")

	resp, err := srv.ListTags(ctx, &spoolv1.ListTagsRequest{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.SpoolUrl != "https://spool.test" {
		t.Errorf("expected default spool URL, got %s", resp.SpoolUrl)
	}
	if len(resp.Tags) != 0 {
		t.Errorf("expected 0 tags, got %d", len(resp.Tags))
	}
}

func TestSpoolServer_ListTags_CustomSpoolURL(t *testing.T) {
	srv := NewSpoolServer("https://spool.test", &config.Config{}, &sync.Mutex{}, nil, registry.New())
	ctx := callerid.Set(context.Background(), "admin:test")

	resp, err := srv.ListTags(ctx, &spoolv1.ListTagsRequest{SpoolUrl: "https://other.test"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.SpoolUrl != "https://other.test" {
		t.Errorf("expected custom spool URL, got %s", resp.SpoolUrl)
	}
}

func TestSpoolServer_DeployTag_EmptyTagName(t *testing.T) {
	srv := NewSpoolServer("https://spool.test", &config.Config{}, &sync.Mutex{}, nil, registry.New())
	ctx := callerid.Set(context.Background(), "admin:test")

	_, err := srv.DeployTag(ctx, &spoolv1.DeployTagRequest{TagName: ""})
	if err == nil {
		t.Fatal("expected error for empty tag_name")
	}
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("expected InvalidArgument, got %s", status.Code(err))
	}
}

func TestSpoolServer_DeployTag_AuthRequired(t *testing.T) {
	srv := NewSpoolServer("https://spool.test", &config.Config{}, &sync.Mutex{}, nil, registry.New())
	ctx := context.Background()

	_, err := srv.DeployTag(ctx, &spoolv1.DeployTagRequest{TagName: "v1.0"})
	if err == nil {
		t.Fatal("expected auth error")
	}
	if status.Code(err) != codes.PermissionDenied {
		t.Errorf("expected PermissionDenied, got %s", status.Code(err))
	}
}

func TestSpoolServer_DeployTag_RateLimited(t *testing.T) {
	reg := registry.New()
	cfg := &config.Config{}
	srv := NewSpoolServer("https://spool.test", cfg, &sync.Mutex{}, nil, reg)
	ctx := callerid.Set(context.Background(), "admin:test")

	_, err1 := srv.DeployTag(ctx, &spoolv1.DeployTagRequest{TagName: "v1.0"})
	if err1 == nil {
		t.Fatal("expected first deploy to fail at spool fetch")
	}
	if status.Code(err1) != codes.Unavailable {
		t.Fatalf("expected Unavailable from first deploy, got %s", status.Code(err1))
	}

	_, err2 := srv.DeployTag(ctx, &spoolv1.DeployTagRequest{TagName: "v1.0"})
	if err2 == nil {
		t.Fatal("expected second deploy to be rate limited")
	}
	if status.Code(err2) != codes.ResourceExhausted {
		t.Errorf("expected ResourceExhausted, got %s", status.Code(err2))
	}
}

func TestSpoolServer_DeployTag_RateLimitDifferentCallers(t *testing.T) {
	reg := registry.New()
	cfg := &config.Config{}
	srv := NewSpoolServer("https://spool.test", cfg, &sync.Mutex{}, nil, reg)

	ctx1 := callerid.Set(context.Background(), "admin:user1")
	ctx2 := callerid.Set(context.Background(), "admin:user2")

	_, err1 := srv.DeployTag(ctx1, &spoolv1.DeployTagRequest{TagName: "v1.0"})
	if err1 == nil {
		t.Fatal("expected first deploy to fail at spool fetch")
	}
	if status.Code(err1) != codes.Unavailable {
		t.Fatalf("expected Unavailable, got %s", status.Code(err1))
	}

	_, err2 := srv.DeployTag(ctx2, &spoolv1.DeployTagRequest{TagName: "v1.0"})
	if err2 == nil {
		t.Fatal("expected second deploy from different caller to fail at spool fetch, not rate limited")
	}
	if status.Code(err2) != codes.Unavailable {
		t.Errorf("expected Unavailable (not rate limited), got %s", status.Code(err2))
	}
}

func TestLifecycleServer_SpawnModule_EmptyRepo(t *testing.T) {
	reg := registry.New()
	srv := NewLifecycleServer(reg, nil, "node-1", nil)
	ctx := callerid.Set(context.Background(), "admin:test")

	_, err := srv.SpawnModule(ctx, &lifecyclev1.SpawnModuleRequest{Repo: ""})
	if err == nil {
		t.Fatal("expected error for empty repo")
	}
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("expected InvalidArgument, got %s", status.Code(err))
	}
}

func TestLifecycleServer_SpawnModule_AuthRequired(t *testing.T) {
	reg := registry.New()
	srv := NewLifecycleServer(reg, nil, "node-1", nil)
	ctx := context.Background()

	_, err := srv.SpawnModule(ctx, &lifecyclev1.SpawnModuleRequest{Repo: "https://github.com/test/mod"})
	if err == nil {
		t.Fatal("expected auth error")
	}
	if status.Code(err) != codes.PermissionDenied {
		t.Errorf("expected PermissionDenied, got %s", status.Code(err))
	}
}

func TestLifecycleServer_SpawnModule_AlreadyRunning(t *testing.T) {
	reg := registry.New()
	srv := NewLifecycleServer(reg, nil, "node-1", nil)

	registerTestModule(t, reg, "mymod", "provider", nil)

	ctx := callerid.Set(context.Background(), "admin:test")

	resp, err := srv.SpawnModule(ctx, &lifecyclev1.SpawnModuleRequest{
		Repo:    "https://github.com/test/mymod",
		Version: "1.0.0",
		TagName: "v1",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Accepted {
		t.Error("expected accepted=false for already running module")
	}
	if resp.Error == "" {
		t.Error("expected error message for already running module")
	}
}

func TestLifecycleServer_SpawnModule_EmptyTagName(t *testing.T) {
	reg := registry.New()
	srv := NewLifecycleServer(reg, nil, "node-1", nil)
	ctx := callerid.Set(context.Background(), "admin:test")

	resp, err := srv.SpawnModule(ctx, &lifecyclev1.SpawnModuleRequest{
		Repo:    "https://github.com/test/newmod",
		Version: "1.0.0",
		TagName: "",
	})
	_ = resp
	if err == nil {
		t.Fatal("expected error for empty tag_name")
	}
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("expected InvalidArgument, got %s", status.Code(err))
	}
}

func TestLifecycleServer_RestartModule_AuthRequired(t *testing.T) {
	srv := NewLifecycleServer(registry.New(), nil, "node-1", nil)
	ctx := context.Background()

	_, err := srv.RestartModule(ctx, &lifecyclev1.RestartModuleRequest{ModuleId: "mod-a"})
	if err == nil {
		t.Fatal("expected auth error")
	}
	if status.Code(err) != codes.PermissionDenied {
		t.Errorf("expected PermissionDenied, got %s", status.Code(err))
	}
}

func TestLifecycleServer_StopModule_AuthRequired(t *testing.T) {
	srv := NewLifecycleServer(registry.New(), nil, "node-1", nil)
	ctx := context.Background()

	_, err := srv.StopModule(ctx, &lifecyclev1.StopModuleRequest{ModuleId: "mod-a"})
	if err == nil {
		t.Fatal("expected auth error")
	}
	if status.Code(err) != codes.PermissionDenied {
		t.Errorf("expected PermissionDenied, got %s", status.Code(err))
	}
}

func TestLifecycleServer_ListModules_AuthRequired(t *testing.T) {
	srv := NewLifecycleServer(registry.New(), nil, "node-1", nil)
	ctx := context.Background()

	_, err := srv.ListModules(ctx, &lifecyclev1.ListModulesRequest{})
	if err == nil {
		t.Fatal("expected auth error")
	}
	if status.Code(err) != codes.PermissionDenied {
		t.Errorf("expected PermissionDenied, got %s", status.Code(err))
	}
}

func TestLifecycleServer_ListModules_EmptyState(t *testing.T) {
	reg := registry.New()
	srv := NewLifecycleServer(reg, nil, "node-1", nil)
	registerTestModule(t, reg, "mod-a", "provider", nil)

	ctx := callerid.Set(context.Background(), "admin:test")

	resp, err := srv.ListModules(ctx, &lifecyclev1.ListModulesRequest{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Total != 1 {
		t.Fatalf("expected 1 module, got %d", resp.Total)
	}
	if resp.Modules[0].State != "registered" {
		t.Errorf("expected state 'registered', got %s", resp.Modules[0].State)
	}
}
