package mock

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/Muxcore-Media/core/pkg/contracts"
)

// --- Interface Compliance ---

var _ contracts.EventBus = (*EventBus)(nil)
var _ contracts.Registry = (*Registry)(nil)
var _ contracts.AuditLogger = (*Audit)(nil)
var _ contracts.ModuleMeshClient = (*Mesh)(nil)
var _ contracts.StorageOrchestrator = (*Storage)(nil)

// --- Test Helpers ---

type testModule struct {
	info contracts.ModuleInfo
}

func (m *testModule) Info() contracts.ModuleInfo       { return m.info }
func (m *testModule) Init(ctx context.Context) error   { return nil }
func (m *testModule) Start(ctx context.Context) error  { return nil }
func (m *testModule) Stop(ctx context.Context) error   { return nil }
func (m *testModule) Health(ctx context.Context) error { return nil }

func newModule(id string, roles, caps []string) *testModule {
	return &testModule{
		info: contracts.ModuleInfo{
			ID:           id,
			Name:         id,
			Version:      "1.0.0",
			Roles:        roles,
			Capabilities: caps,
		},
	}
}

func ctx() context.Context {
	return context.Background()
}

func newTestEvent(id, typ string) contracts.Event {
	return contracts.Event{
		ID:        id,
		Type:      typ,
		Source:    "test",
		TraceID:   "trace-1",
		Payload:   []byte(`{"key":"value"}`),
		Metadata:  map[string]string{"k": "v"},
		Timestamp: time.Now(),
	}
}

func hashPrev(prev contracts.AuditEntry) string {
	h := sha256.New()
	fmt.Fprintf(h, "%s|%s|%s|%s|%s",
		prev.ID, prev.Actor, prev.Action, prev.Resource, prev.Timestamp.Format(time.RFC3339Nano))
	return fmt.Sprintf("%x", h.Sum(nil))
}

// --- EventBus ---

func TestEventBus_PublishDeliversToMatchingSubscribers(t *testing.T) {
	bus := NewEventBus()
	received := make(chan contracts.Event, 4)

	_, err := bus.Subscribe(ctx(), "test.event", func(_ context.Context, e contracts.Event) error {
		received <- e
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	evt := newTestEvent("id-1", "test.event")
	if err := bus.Publish(ctx(), evt); err != nil {
		t.Fatal(err)
	}

	select {
	case got := <-received:
		if got.ID != "id-1" {
			t.Fatalf("expected event id-1, got %s", got.ID)
		}
	case <-time.After(time.Second):
		t.Fatal("handler was not called")
	}
}

func TestEventBus_PublishDoesNotDeliverToNonMatchingSubscribers(t *testing.T) {
	bus := NewEventBus()
	received := make(chan contracts.Event, 1)

	bus.Subscribe(ctx(), "other.event", func(_ context.Context, e contracts.Event) error {
		received <- e
		return nil
	})

	evt := newTestEvent("id-1", "test.event")
	bus.Publish(ctx(), evt)

	select {
	case <-received:
		t.Fatal("handler should not have been called for non-matching event type")
	case <-time.After(50 * time.Millisecond):
	}
}

func TestEventBus_SubscribeWildcard(t *testing.T) {
	bus := NewEventBus()
	var mu sync.Mutex
	var got []string

	_, err := bus.Subscribe(ctx(), "*", func(_ context.Context, e contracts.Event) error {
		mu.Lock()
		got = append(got, e.Type)
		mu.Unlock()
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	bus.Publish(ctx(), newTestEvent("1", "alpha"))
	bus.Publish(ctx(), newTestEvent("2", "beta"))
	bus.Publish(ctx(), newTestEvent("3", "gamma"))

	time.Sleep(50 * time.Millisecond)

	mu.Lock()
	if len(got) != 3 {
		t.Fatalf("expected 3 events, got %d: %v", len(got), got)
	}
	mu.Unlock()
}

func TestEventBus_SubscribeModule(t *testing.T) {
	bus := NewEventBus()
	received := make(chan contracts.Event, 1)

	_, err := bus.SubscribeModule(ctx(), "mod-a", "test.event",
		func(_ context.Context, e contracts.Event) error {
			received <- e
			return nil
		})
	if err != nil {
		t.Fatal(err)
	}

	bus.Publish(ctx(), newTestEvent("1", "test.event"))

	select {
	case <-received:
	case <-time.After(time.Second):
		t.Fatal("SubscribeModule handler was not invoked")
	}
}

func TestEventBus_RequestReplyRoundTrip(t *testing.T) {
	bus := NewEventBus()
	replyPayload := []byte(`{"result":"ok"}`)

	bus.Subscribe(ctx(), "compute", func(_ context.Context, e contracts.Event) error {
		reply := contracts.Event{
			ID:      e.ID + ".reply",
			Type:    contracts.ReplyEventType("compute"),
			Payload: replyPayload,
		}
		return bus.Publish(ctx(), reply)
	})

	req := newTestEvent("req-1", "compute")
	resp, err := bus.Request(ctx(), req, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if string(resp.Payload) != string(replyPayload) {
		t.Fatalf("expected payload %q, got %q", replyPayload, resp.Payload)
	}
}

func TestEventBus_RequestTimeout(t *testing.T) {
	bus := NewEventBus()
	req := newTestEvent("req-timeout", "no.reply")

	_, err := bus.Request(ctx(), req, 10*time.Millisecond)
	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
}

func TestEventBus_CancelSubscription(t *testing.T) {
	bus := NewEventBus()
	received := make(chan contracts.Event, 2)

	cancel, err := bus.Subscribe(ctx(), "test.event",
		func(_ context.Context, e contracts.Event) error {
			received <- e
			return nil
		})
	if err != nil {
		t.Fatal(err)
	}

	bus.Publish(ctx(), newTestEvent("1", "test.event"))
	select {
	case <-received:
	case <-time.After(time.Second):
		t.Fatal("handler not called before cancel")
	}

	cancel()

	bus.Publish(ctx(), newTestEvent("2", "test.event"))
	select {
	case <-received:
		t.Fatal("handler called after cancel")
	case <-time.After(50 * time.Millisecond):
	}
}

func TestEventBus_PublishedEvents(t *testing.T) {
	bus := NewEventBus()
	bus.Publish(ctx(), newTestEvent("1", "a"))
	bus.Publish(ctx(), newTestEvent("2", "b"))

	events := bus.PublishedEvents()
	if len(events) != 2 {
		t.Fatalf("expected 2 published events, got %d", len(events))
	}
	if events[0].ID != "1" || events[1].ID != "2" {
		t.Fatal("published events not in expected order")
	}
}

func TestEventBus_PublishedEventsReturnsCopy(t *testing.T) {
	bus := NewEventBus()
	bus.Publish(ctx(), newTestEvent("1", "a"))

	events := bus.PublishedEvents()
	events[0].ID = "tampered"

	original := bus.PublishedEvents()
	if original[0].ID == "tampered" {
		t.Fatal("PublishedEvents should return a copy")
	}
}

func TestEventBus_HandlerCount(t *testing.T) {
	bus := NewEventBus()
	bus.Subscribe(ctx(), "a", func(_ context.Context, e contracts.Event) error { return nil })
	bus.Subscribe(ctx(), "b", func(_ context.Context, e contracts.Event) error { return nil })
	bus.Subscribe(ctx(), "a", func(_ context.Context, e contracts.Event) error { return nil })

	if n := bus.HandlerCount("a"); n != 2 {
		t.Fatalf("expected 2 handlers for 'a', got %d", n)
	}
	if n := bus.HandlerCount("b"); n != 1 {
		t.Fatalf("expected 1 handler for 'b', got %d", n)
	}
	if n := bus.HandlerCount("c"); n != 0 {
		t.Fatalf("expected 0 handlers for 'c', got %d", n)
	}
}

func TestEventBus_Reset(t *testing.T) {
	bus := NewEventBus()
	bus.Subscribe(ctx(), "a", func(_ context.Context, e contracts.Event) error { return nil })
	bus.Publish(ctx(), newTestEvent("1", "a"))

	bus.Reset()

	if len(bus.PublishedEvents()) != 0 {
		t.Fatal("Reset should clear published events")
	}
	if bus.HandlerCount("a") != 0 {
		t.Fatal("Reset should clear subscriptions")
	}
	if len(bus.HandlerErrors) != 0 {
		t.Fatal("Reset should clear handler errors")
	}
}

func TestEventBus_ConcurrentPublishSubscribe(t *testing.T) {
	bus := NewEventBus()
	var handlerErr error

	bus.Subscribe(ctx(), "conc", func(_ context.Context, e contracts.Event) error {
		time.Sleep(time.Microsecond)
		return nil
	})

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			id := fmt.Sprintf("conc-%d", n)
			if err := bus.Publish(ctx(), newTestEvent(id, "conc")); err != nil {
				handlerErr = err
			}
		}(i)
	}
	wg.Wait()

	if handlerErr != nil {
		t.Fatal(handlerErr)
	}
	if n := len(bus.PublishedEvents()); n != 50 {
		t.Fatalf("expected 50 published events, got %d", n)
	}
}

func TestEventBus_ConcurrentSubscribePublishUnsubscribe(t *testing.T) {
	bus := NewEventBus()
	var wg sync.WaitGroup

	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			cancel, _ := bus.Subscribe(ctx(), fmt.Sprintf("t.%d", n),
				func(_ context.Context, e contracts.Event) error { return nil })
			bus.Publish(ctx(), newTestEvent(fmt.Sprintf("e-%d", n), fmt.Sprintf("t.%d", n)))
			cancel()
		}(i)
	}
	wg.Wait()
}

func TestEventBus_HandlerErrorRecording(t *testing.T) {
	bus := NewEventBus()
	bus.Subscribe(ctx(), "err", func(_ context.Context, e contracts.Event) error {
		return fmt.Errorf("oops")
	})
	bus.Publish(ctx(), newTestEvent("1", "err"))

	if len(bus.HandlerErrors) != 1 {
		t.Fatalf("expected 1 handler error, got %d", len(bus.HandlerErrors))
	}
	if bus.HandlerErrors[0].Error() != "oops" {
		t.Fatalf("unexpected error: %v", bus.HandlerErrors[0])
	}
}

func TestEventBus_UnsubscribeAll(t *testing.T) {
	bus := NewEventBus()
	bus.SubscribeModule(ctx(), "mod-a", "a", func(_ context.Context, e contracts.Event) error { return nil })
	bus.SubscribeModule(ctx(), "mod-a", "b", func(_ context.Context, e contracts.Event) error { return nil })
	bus.SubscribeModule(ctx(), "mod-b", "a", func(_ context.Context, e contracts.Event) error { return nil })

	if err := bus.UnsubscribeAll(ctx(), "mod-a"); err != nil {
		t.Fatal(err)
	}

	if n := bus.HandlerCount("a"); n != 1 {
		t.Fatalf("expected 1 handler for 'a' after removing mod-a, got %d", n)
	}
	if n := bus.HandlerCount("b"); n != 0 {
		t.Fatalf("expected 0 handlers for 'b' after removing mod-a, got %d", n)
	}
}

func TestEventBus_UnsubscribeAllEmpty(t *testing.T) {
	bus := NewEventBus()
	if err := bus.UnsubscribeAll(ctx(), ""); err != nil {
		t.Fatal(err)
	}
}

// --- Registry ---

func TestRegistry_RegisterModule(t *testing.T) {
	r := NewRegistry()
	mod := newModule("mod-a", []string{"worker"}, []string{"compute"})

	if err := r.RegisterModule(mod); err != nil {
		t.Fatal(err)
	}

	entry, err := r.Resolve("mod-a")
	if err != nil {
		t.Fatal(err)
	}
	if entry.Info.ID != "mod-a" {
		t.Fatalf("expected mod-a, got %s", entry.Info.ID)
	}
}

func TestRegistry_DuplicateRegistration(t *testing.T) {
	r := NewRegistry()
	r.RegisterModule(newModule("mod-a", nil, nil))

	err := r.RegisterModule(newModule("mod-a", nil, nil))
	if err == nil {
		t.Fatal("expected error for duplicate registration")
	}
}

func TestRegistry_FindByRole(t *testing.T) {
	r := NewRegistry()
	r.RegisterModule(newModule("worker-1", []string{"worker"}, nil))
	r.RegisterModule(newModule("worker-2", []string{"worker"}, nil))
	r.RegisterModule(newModule("indexer-1", []string{"indexer"}, nil))

	workers := r.FindByRole("worker")
	if len(workers) != 2 {
		t.Fatalf("expected 2 workers, got %d", len(workers))
	}

	indexers := r.FindByRole("indexer")
	if len(indexers) != 1 {
		t.Fatalf("expected 1 indexer, got %d", len(indexers))
	}
}

func TestRegistry_FindByRoleEmpty(t *testing.T) {
	r := NewRegistry()
	got := r.FindByRole("nonexistent")
	if got != nil {
		t.Fatalf("expected nil, got %v", got)
	}
}

func TestRegistry_FindByCapability(t *testing.T) {
	r := NewRegistry()
	r.RegisterModule(newModule("compute-a", nil, []string{"compute", "storage"}))
	r.RegisterModule(newModule("compute-b", nil, []string{"compute"}))
	r.RegisterModule(newModule("storage-1", nil, []string{"storage"}))

	compute := r.FindByCapability("compute")
	if len(compute) != 2 {
		t.Fatalf("expected 2 modules with compute capability, got %d", len(compute))
	}

	storage := r.FindByCapability("storage")
	if len(storage) != 2 {
		t.Fatalf("expected 2 modules with storage capability, got %d", len(storage))
	}
}

func TestRegistry_FindByCapabilityEmpty(t *testing.T) {
	r := NewRegistry()
	got := r.FindByCapability("missing")
	if got != nil {
		t.Fatalf("expected nil, got %v", got)
	}
}

func TestRegistry_SupportsCapability(t *testing.T) {
	r := NewRegistry()
	r.RegisterModule(newModule("mod-a", nil, []string{"compute", "network"}))

	if !r.SupportsCapability("mod-a", "compute") {
		t.Fatal("expected mod-a to support compute")
	}
	if !r.SupportsCapability("mod-a", "network") {
		t.Fatal("expected mod-a to support network")
	}
	if r.SupportsCapability("mod-a", "storage") {
		t.Fatal("expected mod-a to NOT support storage")
	}
	if r.SupportsCapability("unknown", "compute") {
		t.Fatal("expected unknown module to NOT support anything")
	}
}

func TestRegistry_StartupOrder(t *testing.T) {
	r := NewRegistry()
	r.RegisterModule(newModule("z-mod", nil, nil))
	r.RegisterModule(newModule("a-mod", nil, nil))
	r.RegisterModule(newModule("m-mod", nil, nil))

	order, err := r.StartupOrder()
	if err != nil {
		t.Fatal(err)
	}

	expected := map[string]bool{"z-mod": true, "a-mod": true, "m-mod": true}
	if len(order) != len(expected) {
		t.Fatalf("expected %d modules in order, got %d", len(expected), len(order))
	}
	for _, id := range order {
		if !expected[id] {
			t.Fatalf("unexpected module ID in order: %s", id)
		}
	}
}

func TestRegistry_StartupOrderEmpty(t *testing.T) {
	r := NewRegistry()
	order, err := r.StartupOrder()
	if err != nil {
		t.Fatal(err)
	}
	if len(order) != 0 {
		t.Fatalf("expected empty order, got %v", order)
	}
}

func TestRegistry_Resolve(t *testing.T) {
	r := NewRegistry()
	r.RegisterModule(newModule("mod-a", nil, nil))

	entry, err := r.Resolve("mod-a")
	if err != nil {
		t.Fatal(err)
	}
	if entry.Info.ID != "mod-a" {
		t.Fatalf("expected mod-a, got %s", entry.Info.ID)
	}
}

func TestRegistry_ResolveNotFound(t *testing.T) {
	r := NewRegistry()
	_, err := r.Resolve("missing")
	if err == nil {
		t.Fatal("expected error for missing module")
	}
}

func TestRegistry_ListAll(t *testing.T) {
	r := NewRegistry()
	r.RegisterModule(newModule("a", nil, nil))
	r.RegisterModule(newModule("b", nil, nil))

	all := r.ListAll()
	if len(all) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(all))
	}
}

func TestRegistry_ListAllEmpty(t *testing.T) {
	r := NewRegistry()
	all := r.ListAll()
	if all == nil {
		all = []contracts.ModuleEntry{}
	}
	if len(all) != 0 {
		t.Fatalf("expected 0 entries, got %d", len(all))
	}
}

func TestRegistry_DependencyGraphNotFound(t *testing.T) {
	r := NewRegistry()
	_, err := r.DependencyGraph("missing")
	if err == nil {
		t.Fatal("expected error for missing module")
	}
}

func TestRegistry_DependencyGraphReturnsNotImplemented(t *testing.T) {
	r := NewRegistry()
	r.RegisterModule(newModule("mod-a", nil, nil))
	_, err := r.DependencyGraph("mod-a")
	if !errors.Is(err, ErrNotImplemented) {
		t.Fatalf("expected ErrNotImplemented, got %v", err)
	}
}

func TestRegistry_ConcurrentAccess(t *testing.T) {
	r := NewRegistry()
	var wg sync.WaitGroup

	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			mod := newModule(fmt.Sprintf("mod-%d", n), []string{"worker"}, []string{"compute"})
			r.RegisterModule(mod)
		}(i)
	}
	wg.Wait()

	all := r.ListAll()
	if len(all) != 20 {
		t.Fatalf("expected 20 modules, got %d", len(all))
	}
}

// --- Audit ---

func TestAudit_LogAndCount(t *testing.T) {
	a := NewAudit()
	entry := contracts.AuditEntry{
		ID: "e1", Actor: "user-a", Action: "read", Resource: "/file",
		Timestamp: time.Now(),
	}
	if err := a.Log(ctx(), entry); err != nil {
		t.Fatal(err)
	}
	if n := a.Count(); n != 1 {
		t.Fatalf("expected 1 entry, got %d", n)
	}
}

func TestAudit_QueryFilterByActor(t *testing.T) {
	a := NewAudit()
	now := time.Now()
	a.Log(ctx(), contracts.AuditEntry{ID: "1", Actor: "alice", Action: "read", Timestamp: now})
	a.Log(ctx(), contracts.AuditEntry{ID: "2", Actor: "bob", Action: "write", Timestamp: now})
	a.Log(ctx(), contracts.AuditEntry{ID: "3", Actor: "alice", Action: "delete", Timestamp: now})

	results, err := a.Query(ctx(), contracts.AuditFilter{Actor: "alice"})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 alice entries, got %d", len(results))
	}
	for _, r := range results {
		if r.Actor != "alice" {
			t.Fatalf("expected actor alice, got %s", r.Actor)
		}
	}
}

func TestAudit_QueryFilterByAction(t *testing.T) {
	a := NewAudit()
	now := time.Now()
	a.Log(ctx(), contracts.AuditEntry{ID: "1", Actor: "a", Action: "read", Timestamp: now})
	a.Log(ctx(), contracts.AuditEntry{ID: "2", Actor: "b", Action: "write", Timestamp: now})

	results, err := a.Query(ctx(), contracts.AuditFilter{Action: "read"})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
}

func TestAudit_QueryFilterByTimeRange(t *testing.T) {
	a := NewAudit()
	base := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	a.Log(ctx(), contracts.AuditEntry{ID: "1", Timestamp: base})
	a.Log(ctx(), contracts.AuditEntry{ID: "2", Timestamp: base.Add(time.Hour)})
	a.Log(ctx(), contracts.AuditEntry{ID: "3", Timestamp: base.Add(2 * time.Hour)})

	results, err := a.Query(ctx(), contracts.AuditFilter{
		From: base.Add(30 * time.Minute),
		To:   base.Add(90 * time.Minute),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].ID != "2" {
		t.Fatalf("expected 1 entry (id=2), got %d entries", len(results))
	}
}

func TestAudit_QueryFilterByResource(t *testing.T) {
	a := NewAudit()
	a.Log(ctx(), contracts.AuditEntry{ID: "1", Resource: "r1"})
	a.Log(ctx(), contracts.AuditEntry{ID: "2", Resource: "r2"})

	results, err := a.Query(ctx(), contracts.AuditFilter{Resource: "r1"})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].ID != "1" {
		t.Fatal("expected 1 result with id=1")
	}
}

func TestAudit_QueryFilterByTraceID(t *testing.T) {
	a := NewAudit()
	a.Log(ctx(), contracts.AuditEntry{ID: "1", TraceID: "trace-a"})
	a.Log(ctx(), contracts.AuditEntry{ID: "2", TraceID: "trace-b"})

	results, err := a.Query(ctx(), contracts.AuditFilter{TraceID: "trace-a"})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].ID != "1" {
		t.Fatal("expected 1 result with id=1")
	}
}

func TestAudit_QueryNoMatch(t *testing.T) {
	a := NewAudit()
	a.Log(ctx(), contracts.AuditEntry{ID: "1", Actor: "a"})

	results, err := a.Query(ctx(), contracts.AuditFilter{Actor: "nonexistent"})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 0 {
		t.Fatalf("expected 0 results, got %d", len(results))
	}
}

func TestAudit_ExportJSON(t *testing.T) {
	a := NewAudit()
	now := time.Now()
	a.Log(ctx(), contracts.AuditEntry{ID: "e1", Actor: "a", Action: "read", Timestamp: now})

	rc, err := a.Export(ctx(), "json")
	if err != nil {
		t.Fatal(err)
	}
	defer rc.Close()

	data, err := io.ReadAll(rc)
	if err != nil {
		t.Fatal(err)
	}

	var entries []contracts.AuditEntry
	if err := json.Unmarshal(data, &entries); err != nil {
		t.Fatalf("invalid JSON export: %v", err)
	}
	if len(entries) != 1 || entries[0].ID != "e1" {
		t.Fatal("exported JSON data mismatch")
	}
}

func TestAudit_ExportCSV(t *testing.T) {
	a := NewAudit()
	now := time.Date(2025, 6, 1, 12, 0, 0, 0, time.UTC)
	a.Log(ctx(), contracts.AuditEntry{
		ID: "e1", Actor: "alice", Action: "read", Resource: "/file",
		Timestamp: now,
	})

	rc, err := a.Export(ctx(), "csv")
	if err != nil {
		t.Fatal(err)
	}
	defer rc.Close()

	data, err := io.ReadAll(rc)
	if err != nil {
		t.Fatal(err)
	}

	output := string(data)
	if len(output) == 0 {
		t.Fatal("empty CSV export")
	}
}

func TestAudit_ExportUnsupportedFormat(t *testing.T) {
	a := NewAudit()
	_, err := a.Export(ctx(), "xml")
	if err == nil {
		t.Fatal("expected error for unsupported format")
	}
}

func TestAudit_VerifyChainIntegrityClean(t *testing.T) {
	a := NewAudit()
	now := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)

	e1 := contracts.AuditEntry{ID: "1", Actor: "a", Action: "read", Resource: "r1", Timestamp: now}
	a.Log(ctx(), e1)

	e2 := contracts.AuditEntry{ID: "2", Actor: "b", Action: "write", Resource: "r2",
		Timestamp: now.Add(time.Minute), PrevEntryHash: hashPrev(e1)}
	a.Log(ctx(), e2)

	e3 := contracts.AuditEntry{ID: "3", Actor: "a", Action: "delete", Resource: "r3",
		Timestamp: now.Add(2 * time.Minute), PrevEntryHash: hashPrev(e2)}
	a.Log(ctx(), e3)

	result, err := a.VerifyChainIntegrity(ctx(), time.Time{}, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Valid {
		t.Fatalf("expected valid chain, broken links: %v", result.BrokenLinks)
	}
	if result.TotalEntries != 3 {
		t.Fatalf("expected 3 entries, got %d", result.TotalEntries)
	}
}

func TestAudit_VerifyChainIntegrityTampered(t *testing.T) {
	a := NewAudit()
	now := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)

	e1 := contracts.AuditEntry{ID: "1", Actor: "a", Action: "read", Resource: "r1", Timestamp: now}
	a.Log(ctx(), e1)

	e2 := contracts.AuditEntry{ID: "2", Actor: "b", Action: "write", Resource: "r2",
		Timestamp: now.Add(time.Minute), PrevEntryHash: hashPrev(e1)}
	a.Log(ctx(), e2)

	e3 := contracts.AuditEntry{ID: "3", Actor: "a", Action: "delete", Resource: "r3",
		Timestamp: now.Add(2 * time.Minute), PrevEntryHash: "tampered-hash"}
	a.Log(ctx(), e3)

	result, err := a.VerifyChainIntegrity(ctx(), time.Time{}, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if result.Valid {
		t.Fatal("expected invalid chain for tampered entry")
	}
	if len(result.BrokenLinks) != 1 || result.BrokenLinks[0] != "3" {
		t.Fatalf("expected broken link at entry 3, got %v", result.BrokenLinks)
	}
}

func TestAudit_VerifyChainIntegritySingleEntry(t *testing.T) {
	a := NewAudit()
	now := time.Now()
	a.Log(ctx(), contracts.AuditEntry{ID: "1", Actor: "a", Timestamp: now})

	result, err := a.VerifyChainIntegrity(ctx(), time.Time{}, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Valid {
		t.Fatal("single entry chain should be valid")
	}
	if result.TotalEntries != 1 {
		t.Fatalf("expected 1 entry, got %d", result.TotalEntries)
	}
}

func TestAudit_VerifyChainIntegrityTimeRange(t *testing.T) {
	a := NewAudit()
	now := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)

	e1 := contracts.AuditEntry{ID: "1", Timestamp: now}
	a.Log(ctx(), e1)

	e2 := contracts.AuditEntry{ID: "2", Timestamp: now.Add(time.Hour), PrevEntryHash: hashPrev(e1)}
	a.Log(ctx(), e2)

	result, err := a.VerifyChainIntegrity(ctx(), now.Add(30*time.Minute), now.Add(90*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if !result.Valid {
		t.Fatal("expected valid chain for time range")
	}
	if result.TotalEntries != 1 {
		t.Fatalf("expected 1 entry in range, got %d", result.TotalEntries)
	}
}

func TestAudit_MultipleSequentialLogs(t *testing.T) {
	a := NewAudit()
	now := time.Now()

	for i := 0; i < 100; i++ {
		entry := contracts.AuditEntry{
			ID: fmt.Sprintf("seq-%d", i), Actor: "user", Action: "op",
			Timestamp: now.Add(time.Duration(i) * time.Millisecond),
		}
		if err := a.Log(ctx(), entry); err != nil {
			t.Fatal(err)
		}
	}

	if n := a.Count(); n != 100 {
		t.Fatalf("expected 100 entries, got %d", n)
	}
}

func TestAudit_ConcurrentLogAndQuery(t *testing.T) {
	a := NewAudit()
	var wg sync.WaitGroup

	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			a.Log(ctx(), contracts.AuditEntry{
				ID: fmt.Sprintf("c-%d", n), Actor: "tester",
				Timestamp: time.Now(),
			})
		}(i)
	}
	wg.Wait()

	if n := a.Count(); n != 50 {
		t.Fatalf("expected 50 entries, got %d", n)
	}

	results, err := a.Query(ctx(), contracts.AuditFilter{Actor: "tester"})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 50 {
		t.Fatalf("expected 50 results, got %d", len(results))
	}
}

// --- ModuleMeshClient ---

func TestMesh_CallSendsViaHandler(t *testing.T) {
	m := NewMesh()
	m.SetHandler("target-mod", func(_ context.Context, method string, payload []byte) ([]byte, error) {
		return []byte(`{"handled":true}`), nil
	})

	resp, err := m.Call(ctx(), "target-mod", "ping", []byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if string(resp) != `{"handled":true}` {
		t.Fatalf("unexpected response: %s", resp)
	}
}

func TestMesh_RegisterHandler(t *testing.T) {
	m := NewMesh()
	m.RegisterHandler("mod-a", &funcHandler{
		fn: func(_ context.Context, method string, payload []byte) ([]byte, error) {
			return payload, nil
		},
	})

	resp, err := m.Call(ctx(), "mod-a", "echo", []byte("hello"))
	if err != nil {
		t.Fatal(err)
	}
	if string(resp) != "hello" {
		t.Fatalf("expected 'hello', got '%s'", resp)
	}
}

func TestMesh_CallJSON(t *testing.T) {
	m := NewMesh()
	m.SetHandler("calc", func(_ context.Context, method string, payload []byte) ([]byte, error) {
		var req struct{ X, Y int }
		if err := json.Unmarshal(payload, &req); err != nil {
			return nil, err
		}
		resp := struct{ Sum int }{Sum: req.X + req.Y}
		return json.Marshal(resp)
	})

	req := struct{ X, Y int }{3, 4}
	var resp struct{ Sum int }
	err := m.CallJSON(ctx(), "calc", "add", req, &resp)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Sum != 7 {
		t.Fatalf("expected sum 7, got %d", resp.Sum)
	}
}

func TestMesh_CallJSONMarshalError(t *testing.T) {
	m := NewMesh()
	err := m.CallJSON(ctx(), "any", "method", make(chan int), nil)
	if err == nil {
		t.Fatal("expected marshal error")
	}
}

func TestMesh_CallNoHandler(t *testing.T) {
	m := NewMesh()
	_, err := m.Call(ctx(), "unknown", "method", nil)
	if err == nil {
		t.Fatal("expected error for missing handler")
	}
}

func TestMesh_CallRecordsCall(t *testing.T) {
	m := NewMesh()
	m.SetHandler("mod-a", func(_ context.Context, method string, payload []byte) ([]byte, error) {
		return nil, nil
	})

	m.Call(ctx(), "mod-a", "m1", []byte("p1"))
	m.Call(ctx(), "mod-b", "m2", []byte("p2"))

	if len(m.Calls) != 2 {
		t.Fatalf("expected 2 recorded calls, got %d", len(m.Calls))
	}
	if m.Calls[0].TargetModule != "mod-a" || m.Calls[0].Method != "m1" {
		t.Fatal("first call details mismatch")
	}
	if string(m.Calls[0].Payload) != "p1" {
		t.Fatalf("first call payload mismatch: %s", m.Calls[0].Payload)
	}
}

func TestMesh_ConcurrentCalls(t *testing.T) {
	m := NewMesh()
	m.SetHandler("target", func(_ context.Context, method string, payload []byte) ([]byte, error) {
		time.Sleep(time.Microsecond)
		return payload, nil
	})

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			payload := []byte(fmt.Sprintf("req-%d", n))
			resp, err := m.Call(ctx(), "target", "echo", payload)
			if err != nil {
				t.Error(err)
				return
			}
			if string(resp) != string(payload) {
				t.Errorf("payload mismatch")
			}
		}(i)
	}
	wg.Wait()

	if len(m.Calls) != 50 {
		t.Fatalf("expected 50 recorded calls, got %d", len(m.Calls))
	}
}

func TestMesh_CallJSONUnmarshalError(t *testing.T) {
	m := NewMesh()
	m.SetHandler("bad", func(_ context.Context, method string, payload []byte) ([]byte, error) {
		return []byte(`not-json`), nil
	})

	err := m.CallJSON(ctx(), "bad", "m", struct{}{}, &struct{}{})
	if err == nil {
		t.Fatal("expected unmarshal error")
	}
}

// --- Storage ---

func TestStorage_PutAndGet(t *testing.T) {
	s := NewStorage()
	data := []byte("hello storage")

	if err := s.Put(ctx(), "mykey", bytes.NewReader(data), int64(len(data))); err != nil {
		t.Fatal(err)
	}

	rc, err := s.Get(ctx(), "mykey")
	if err != nil {
		t.Fatal(err)
	}
	defer rc.Close()

	got, err := io.ReadAll(rc)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(data) {
		t.Fatalf("expected %q, got %q", data, got)
	}
}

func TestStorage_GetNotFound(t *testing.T) {
	s := NewStorage()
	_, err := s.Get(ctx(), "nonexistent")
	if err == nil {
		t.Fatal("expected error for non-existent key")
	}
}

func TestStorage_PutOverwrites(t *testing.T) {
	s := NewStorage()
	s.Put(ctx(), "k", bytes.NewReader([]byte("first")), 5)
	s.Put(ctx(), "k", bytes.NewReader([]byte("second")), 6)

	rc, _ := s.Get(ctx(), "k")
	defer rc.Close()
	got, _ := io.ReadAll(rc)
	if string(got) != "second" {
		t.Fatalf("expected 'second', got %q", got)
	}
}

func TestStorage_Delete(t *testing.T) {
	s := NewStorage()
	s.Put(ctx(), "k", bytes.NewReader([]byte("data")), 4)

	if err := s.Delete(ctx(), "k"); err != nil {
		t.Fatal(err)
	}

	exists, _ := s.Exists(ctx(), "k")
	if exists {
		t.Fatal("key should not exist after delete")
	}

	_, err := s.Get(ctx(), "k")
	if err == nil {
		t.Fatal("expected error after delete")
	}
}

func TestStorage_DeleteNonExistent(t *testing.T) {
	s := NewStorage()
	if err := s.Delete(ctx(), "ghost"); err != nil {
		t.Fatal(err)
	}
}

func TestStorage_Move(t *testing.T) {
	s := NewStorage()
	data := []byte("movable")
	s.Put(ctx(), "src", bytes.NewReader(data), int64(len(data)))

	if err := s.Move(ctx(), "src", "dst"); err != nil {
		t.Fatal(err)
	}

	exists, _ := s.Exists(ctx(), "src")
	if exists {
		t.Fatal("source should not exist after move")
	}

	rc, err := s.Get(ctx(), "dst")
	if err != nil {
		t.Fatal(err)
	}
	defer rc.Close()
	got, _ := io.ReadAll(rc)
	if string(got) != string(data) {
		t.Fatalf("expected %q at dst, got %q", data, got)
	}
}

func TestStorage_MoveNotFound(t *testing.T) {
	s := NewStorage()
	err := s.Move(ctx(), "missing", "dst")
	if err == nil {
		t.Fatal("expected error for missing source")
	}
}

func TestStorage_Exists(t *testing.T) {
	s := NewStorage()
	s.Put(ctx(), "k", bytes.NewReader([]byte("x")), 1)

	ok, err := s.Exists(ctx(), "k")
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("expected key to exist")
	}

	ok, err = s.Exists(ctx(), "missing")
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("expected key to not exist")
	}
}

func TestStorage_Stat(t *testing.T) {
	s := NewStorage()
	data := []byte("stat-test")
	s.Put(ctx(), "k", bytes.NewReader(data), int64(len(data)))

	info, err := s.Stat(ctx(), "k")
	if err != nil {
		t.Fatal(err)
	}
	if info.Key != "k" {
		t.Fatalf("expected key 'k', got %q", info.Key)
	}
	if info.Size != int64(len(data)) {
		t.Fatalf("expected size %d, got %d", len(data), info.Size)
	}
}

func TestStorage_StatNotFound(t *testing.T) {
	s := NewStorage()
	_, err := s.Stat(ctx(), "missing")
	if err == nil {
		t.Fatal("expected error for missing key")
	}
}

func TestStorage_List(t *testing.T) {
	s := NewStorage()
	s.Put(ctx(), "a/1", bytes.NewReader([]byte("x")), 1)
	s.Put(ctx(), "a/2", bytes.NewReader([]byte("y")), 1)
	s.Put(ctx(), "b/1", bytes.NewReader([]byte("z")), 1)
	s.Put(ctx(), "c/1", bytes.NewReader([]byte("w")), 1)

	all, err := s.List(ctx(), "")
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 4 {
		t.Fatalf("expected 4 objects, got %d", len(all))
	}

	aOnly, err := s.List(ctx(), "a/")
	if err != nil {
		t.Fatal(err)
	}
	if len(aOnly) != 2 {
		t.Fatalf("expected 2 objects with prefix 'a/', got %d", len(aOnly))
	}
	for _, info := range aOnly {
		if len(info.Key) < 2 || info.Key[:2] != "a/" {
			t.Fatalf("unexpected key in prefix results: %s", info.Key)
		}
	}
}

func TestStorage_ListEmptyPrefix(t *testing.T) {
	s := NewStorage()
	results, err := s.List(ctx(), "nonexistent/")
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 0 {
		t.Fatalf("expected 0 results, got %d", len(results))
	}
}

func TestStorage_Stream(t *testing.T) {
	s := NewStorage()
	data := []byte("0123456789")
	s.Put(ctx(), "stream-key", bytes.NewReader(data), int64(len(data)))

	rc, err := s.Stream(ctx(), "stream-key", 3, 4)
	if err != nil {
		t.Fatal(err)
	}
	defer rc.Close()

	got, err := io.ReadAll(rc)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "3456" {
		t.Fatalf("expected '3456', got %q", got)
	}
}

func TestStorage_StreamOffsetPastEnd(t *testing.T) {
	s := NewStorage()
	s.Put(ctx(), "k", bytes.NewReader([]byte("hello")), 5)

	rc, err := s.Stream(ctx(), "k", 100, 10)
	if err != nil {
		t.Fatal(err)
	}
	defer rc.Close()

	got, _ := io.ReadAll(rc)
	if len(got) != 0 {
		t.Fatalf("expected empty stream, got %q", got)
	}
}

func TestStorage_StreamPartialEnd(t *testing.T) {
	s := NewStorage()
	data := []byte("hello world")
	s.Put(ctx(), "k", bytes.NewReader(data), int64(len(data)))

	rc, err := s.Stream(ctx(), "k", 8, 10)
	if err != nil {
		t.Fatal(err)
	}
	defer rc.Close()

	got, _ := io.ReadAll(rc)
	if string(got) != "rld" {
		t.Fatalf("expected 'rld', got %q", got)
	}
}

func TestStorage_StreamNotFound(t *testing.T) {
	s := NewStorage()
	_, err := s.Stream(ctx(), "missing", 0, 10)
	if err == nil {
		t.Fatal("expected error for missing key")
	}
}

func TestStorage_CapabilityCheck(t *testing.T) {
	s := NewStorage()
	caps, err := s.CapabilityCheck(ctx(), "any-key")
	if err != nil {
		t.Fatal(err)
	}
	if len(caps) != 1 || caps[0] != "streamable" {
		t.Fatalf("expected [streamable], got %v", caps)
	}
}

func TestStorage_ConcurrentAccess(t *testing.T) {
	s := NewStorage()
	var wg sync.WaitGroup

	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			key := fmt.Sprintf("key-%d", n)
			data := []byte(fmt.Sprintf("data-%d", n))
			s.Put(ctx(), key, bytes.NewReader(data), int64(len(data)))
		}(i)
	}
	wg.Wait()

	var wg2 sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg2.Add(1)
		go func(n int) {
			defer wg2.Done()
			key := fmt.Sprintf("key-%d", n)
			rc, err := s.Get(ctx(), key)
			if err != nil {
				t.Error(err)
				return
			}
			rc.Close()
		}(i)
	}
	wg2.Wait()

	all, _ := s.List(ctx(), "")
	if len(all) != 50 {
		t.Fatalf("expected 50 keys, got %d", len(all))
	}
}

func TestStorage_ConcurrentReadWrite(t *testing.T) {
	s := NewStorage()
	s.Put(ctx(), "shared", bytes.NewReader([]byte("initial")), 7)

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			data := []byte(fmt.Sprintf("write-%d", n))
			s.Put(ctx(), "shared", bytes.NewReader(data), int64(len(data)))
		}(i)
		wg.Add(1)
		go func() {
			defer wg.Done()
			rc, err := s.Get(ctx(), "shared")
			if err == nil {
				rc.Close()
			}
		}()
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.Stat(ctx(), "shared")
		}()
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.Exists(ctx(), "shared")
		}()
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.List(ctx(), "")
		}()
	}
	wg.Wait()
}
