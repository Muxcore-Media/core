package grpcmesh

import (
	"context"
	"errors"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/Muxcore-Media/core/internal/callerid"
	"github.com/Muxcore-Media/core/internal/events"
	"github.com/Muxcore-Media/core/pkg/contracts"
	eventsv1 "github.com/Muxcore-Media/core/proto/gen/muxcore/events/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// allowAllPublishPolicy permits all event publication for testing.
type allowAllPublishPolicy struct{}

func (allowAllPublishPolicy) CanPublish(_ context.Context, _, _ string) (bool, error) {
	return true, nil
}

// allowCallerPublishPolicy permits only the configured caller module ID.
type allowCallerPublishPolicy struct {
	allowed string
}

func (p allowCallerPublishPolicy) CanPublish(_ context.Context, callerID, _ string) (bool, error) {
	return callerID == p.allowed, nil
}

type eventServerTestConfig struct {
	identityID    string
	publishPolicy contracts.PublishPolicyProvider
	withAuth      bool
}

func startEventServer(t *testing.T) (eventsv1.EventServiceClient, *events.MemoryBus) {
	t.Helper()
	return startEventServerWithConfig(t, eventServerTestConfig{
		identityID:    "test-runner",
		publishPolicy: allowAllPublishPolicy{},
		withAuth:      true,
	})
}

func startEventServerWithReplayer(t *testing.T, replayer WALReplayer) (eventsv1.EventServiceClient, *events.MemoryBus) {
	t.Helper()
	return startEventServerWithConfigAndReplayer(t, eventServerTestConfig{
		identityID:    "test-runner",
		publishPolicy: allowAllPublishPolicy{},
		withAuth:      true,
	}, replayer)
}

func startEventServerWithConfig(t *testing.T, cfg eventServerTestConfig) (eventsv1.EventServiceClient, *events.MemoryBus) {
	t.Helper()
	return startEventServerWithConfigAndReplayer(t, cfg, nil)
}

func startEventServerWithConfigAndReplayer(t *testing.T, cfg eventServerTestConfig, replayer WALReplayer) (eventsv1.EventServiceClient, *events.MemoryBus) {
	t.Helper()

	bus := events.NewMemoryBus()
	if cfg.publishPolicy != nil {
		bus.SetPublishPolicy(cfg.publishPolicy)
	} else {
		bus.SetPublishPolicy(allowAllPublishPolicy{})
	}

	srv := NewEventServer(bus)
	if replayer != nil {
		srv.SetWALReplayer(replayer)
	}
	var lc net.ListenConfig
	lis, err := lc.Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	var grpcOpts []grpc.ServerOption
	if cfg.withAuth {
		auth := NewAuthInterceptor()
		auth.SetAuthorizer(&stubAuthorizer{allow: true})
		if cfg.identityID != "" {
			auth.SetIdentityProvider(&stubIdentityProvider{
				identity: &contracts.Identity{ID: cfg.identityID, Roles: []string{"module"}},
			})
		} else {
			auth.SetIdentityProvider(&stubIdentityProvider{identity: nil})
		}
		grpcOpts = append(grpcOpts,
			grpc.UnaryInterceptor(auth.UnaryInterceptor()),
			grpc.StreamInterceptor(auth.StreamInterceptor()),
		)
	}

	grpcSrv := grpc.NewServer(grpcOpts...)
	srv.RegisterWithGRPC(grpcSrv)
	go grpcSrv.Serve(lis)
	t.Cleanup(grpcSrv.GracefulStop)

	conn, err := grpc.NewClient(lis.Addr().String(),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { conn.Close() })

	return eventsv1.NewEventServiceClient(conn), bus
}

// testAuthCtx returns a baseline context for gRPC calls in tests where the
// auth interceptor supplies verified caller identity.
func testAuthCtx() context.Context {
	return context.Background()
}

// spoofedCallerCtx returns a context that carries a client-spoofable x-caller-id.
func spoofedCallerCtx(callerID string) context.Context {
	return metadata.NewOutgoingContext(context.Background(),
		metadata.Pairs("x-caller-id", callerID))
}

func TestEventServer_Publish_Relay(t *testing.T) {
	client, bus := startEventServer(t)

	received := make(chan contracts.Event, 1)
	bus.Subscribe(context.Background(), "test.relayed", func(ctx context.Context, e contracts.Event) error {
		received <- e
		return nil
	})

	_, err := client.Publish(testAuthCtx(), &eventsv1.PublishRequest{
		Event: &eventsv1.Event{
			Id:      "ev-1",
			Type:    "test.relayed",
			Source:  "remote-node",
			Payload: []byte(`"data"`),
		},
	})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}

	select {
	case ev := <-received:
		if ev.Type != "test.relayed" {
			t.Errorf("expected type 'test.relayed', got %q", ev.Type)
		}
		if string(ev.Payload) != `"data"` {
			t.Errorf("unexpected payload: %s", ev.Payload)
		}
	case <-time.After(2 * time.Second):
		t.Error("timeout waiting for relayed event")
	}
}

func TestEventServer_Subscribe_ReceivesPublished(t *testing.T) {
	client, bus := startEventServer(t)

	ctx, cancel := context.WithTimeout(testAuthCtx(), 5*time.Second)
	defer cancel()

	stream, err := client.Subscribe(ctx, &eventsv1.SubscribeRequest{
		EventTypes: []string{"test.streamed"},
	})
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	// Give the subscription a moment to be registered.
	time.Sleep(50 * time.Millisecond)

	// Publish via the local bus.
	bus.Publish(context.Background(), contracts.Event{
		Type:    "test.streamed",
		Source:  "local",
		Payload: []byte(`42`),
	})

	ev, err := stream.Recv()
	if err != nil {
		t.Fatalf("Recv: %v", err)
	}
	if ev.GetType() != "test.streamed" {
		t.Errorf("expected 'test.streamed', got %q", ev.GetType())
	}
}

func TestEventServer_Subscribe_MultipleTypes(t *testing.T) {
	client, bus := startEventServer(t)

	ctx, cancel := context.WithTimeout(testAuthCtx(), 5*time.Second)
	defer cancel()

	stream, err := client.Subscribe(ctx, &eventsv1.SubscribeRequest{
		EventTypes: []string{"type.a", "type.b"},
	})
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	time.Sleep(50 * time.Millisecond)

	bus.Publish(context.Background(), contracts.Event{Type: "type.a", Source: "x"})
	bus.Publish(context.Background(), contracts.Event{Type: "type.b", Source: "x"})

	types := map[string]bool{}
	for i := 0; i < 2; i++ {
		ev, err := stream.Recv()
		if err != nil && !errors.Is(err, io.EOF) {
			t.Fatalf("Recv %d: %v", i, err)
		}
		types[ev.GetType()] = true
	}

	if !types["type.a"] || !types["type.b"] {
		t.Errorf("expected both type.a and type.b, got %v", types)
	}
}

func TestEventServer_Request_Reply(t *testing.T) {
	client, bus := startEventServer(t)

	// Register a reply handler on the bus.
	bus.Subscribe(context.Background(), "ping", func(ctx context.Context, e contracts.Event) error {
		return bus.Publish(ctx, contracts.Event{
			Type:    "ping.reply",
			Source:  "handler",
			Payload: []byte(`"pong"`),
		})
	})

	time.Sleep(20 * time.Millisecond)

	ctx, cancel := context.WithTimeout(testAuthCtx(), 5*time.Second)
	defer cancel()

	reply, err := client.Request(ctx, &eventsv1.RequestEvent{
		Event: &eventsv1.Event{
			Id:   "req-1",
			Type: "ping",
		},
		TimeoutMs: 3000,
	})
	if err != nil {
		t.Fatalf("Request: %v", err)
	}
	if reply.GetType() != "ping.reply" {
		t.Errorf("expected ping.reply, got %q", reply.GetType())
	}
}

func TestEventServer_Publish_SourceNodeOverridden(t *testing.T) {
	client, bus := startEventServer(t)

	received := make(chan contracts.Event, 1)
	bus.Subscribe(context.Background(), "spoofed.event", func(ctx context.Context, e contracts.Event) error {
		received <- e
		return nil
	})

	// Claim to be source_node "hacker" — server should override with peer addr.
	_, err := client.Publish(testAuthCtx(), &eventsv1.PublishRequest{
		Event: &eventsv1.Event{
			Type:       "spoofed.event",
			SourceNode: "hacker",
		},
	})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}

	select {
	case ev := <-received:
		// The event metadata source_node should be the real peer address, not "hacker".
		srcNode := ev.Metadata["source_node"]
		if srcNode == "hacker" {
			t.Error("source_node should be overridden by real peer address, not kept as 'hacker'")
		}
	case <-time.After(2 * time.Second):
		t.Error("timeout waiting for event")
	}
}

// --- Replay RPC ---

// stubWALReplayer is a configurable WALReplayer for testing.
type stubWALReplayer struct {
	events []contracts.Event
}

func (r *stubWALReplayer) ReplayFrom(_ context.Context, sinceSeq uint64, fn func(contracts.Event) error) error {
	for i, e := range r.events {
		if uint64(i)+1 >= sinceSeq {
			if err := fn(e); err != nil {
				return err
			}
		}
	}
	return nil
}

func TestEventServer_Replay_NoWAL_Unimplemented(t *testing.T) {
	client, _ := startEventServer(t) // no WAL replayer set

	stream, err := client.Replay(testAuthCtx(), &eventsv1.ReplayRequest{SinceSeq: 0})
	if err != nil {
		t.Fatalf("Replay open: %v", err)
	}
	_, err = stream.Recv()
	if err == nil {
		t.Fatal("expected error when no WAL replayer configured")
	}
	st, _ := status.FromError(err)
	if st.Code() != codes.Unimplemented {
		t.Errorf("expected Unimplemented, got %s: %v", st.Code(), err)
	}
}

func TestEventServer_Replay_WithWAL_StreamsEvents(t *testing.T) {
	replayer := &stubWALReplayer{
		events: []contracts.Event{
			{ID: "1", Type: "media.added", Payload: []byte(`"a"`)},
			{ID: "2", Type: "media.added", Payload: []byte(`"b"`)},
			{ID: "3", Type: "media.updated", Payload: []byte(`"c"`)},
		},
	}
	client, _ := startEventServerWithReplayer(t, replayer)

	ctx, cancel := context.WithTimeout(testAuthCtx(), 5*time.Second)
	defer cancel()

	stream, err := client.Replay(ctx, &eventsv1.ReplayRequest{SinceSeq: 0})
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}

	var received []*eventsv1.Event
	for {
		ev, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("Recv: %v", err)
		}
		received = append(received, ev)
	}

	if len(received) != 3 {
		t.Fatalf("expected 3 events, got %d", len(received))
	}
	if received[0].GetId() != "1" || received[2].GetId() != "3" {
		t.Errorf("unexpected event IDs: %v", received)
	}
}

func TestEventServer_Replay_WithWAL_TypeFilter(t *testing.T) {
	replayer := &stubWALReplayer{
		events: []contracts.Event{
			{ID: "1", Type: "media.added"},
			{ID: "2", Type: "media.updated"},
			{ID: "3", Type: "media.added"},
		},
	}
	client, _ := startEventServerWithReplayer(t, replayer)

	ctx, cancel := context.WithTimeout(testAuthCtx(), 5*time.Second)
	defer cancel()

	stream, err := client.Replay(ctx, &eventsv1.ReplayRequest{
		SinceSeq:  0,
		EventType: "media.added",
	})
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}

	var received []*eventsv1.Event
	for {
		ev, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("Recv: %v", err)
		}
		received = append(received, ev)
	}

	if len(received) != 2 {
		t.Fatalf("expected 2 filtered events, got %d", len(received))
	}
	for _, ev := range received {
		if ev.GetType() != "media.added" {
			t.Errorf("expected only 'media.added' events, got %q", ev.GetType())
		}
	}
}

// TestEventServer_Replay_RealWAL uses a real MemoryBus with WAL to test the
// full Publish → WAL persist → Replay RPC path.
func TestEventServer_Replay_RealWAL(t *testing.T) {
	dir := t.TempDir()
	bus := events.NewMemoryBus()
	bus.SetPublishPolicy(allowAllPublishPolicy{})
	if err := bus.EnableWAL(dir); err != nil {
		t.Fatalf("EnableWAL: %v", err)
	}
	t.Cleanup(func() { bus.CloseWAL() })

	// Publish a few events so the WAL has content.
	ctx := context.Background()
	for i := range 3 {
		if err := bus.Publish(ctx, contracts.Event{
			Type:    "wal.test",
			Payload: []byte{byte(i)},
		}); err != nil {
			t.Fatalf("Publish %d: %v", i, err)
		}
	}

	srv := NewEventServer(bus)
	srv.SetWALReplayer(bus)
	var lc net.ListenConfig
	lis, err := lc.Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	auth := NewAuthInterceptor()
	auth.SetAuthorizer(&stubAuthorizer{allow: true})
	auth.SetIdentityProvider(&stubIdentityProvider{
		identity: &contracts.Identity{ID: "test-runner", Roles: []string{"module"}},
	})
	grpcSrv := grpc.NewServer(
		grpc.UnaryInterceptor(auth.UnaryInterceptor()),
		grpc.StreamInterceptor(auth.StreamInterceptor()),
	)
	srv.RegisterWithGRPC(grpcSrv)
	go grpcSrv.Serve(lis)
	t.Cleanup(grpcSrv.GracefulStop)

	conn, err := grpc.NewClient(lis.Addr().String(),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { conn.Close() })

	client := eventsv1.NewEventServiceClient(conn)

	replayCtx, cancel := context.WithTimeout(testAuthCtx(), 5*time.Second)
	defer cancel()

	stream, err := client.Replay(replayCtx, &eventsv1.ReplayRequest{SinceSeq: 0})
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}

	var count int
	for {
		_, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("Recv: %v", err)
		}
		count++
	}

	if count != 3 {
		t.Errorf("expected 3 replayed events, got %d", count)
	}
}

func TestEventServer_CheckVerifiedAuth_RejectsPublicAndEmpty(t *testing.T) {
	srv := NewEventServer(nil)

	tests := []struct {
		name   string
		caller string
		setID  bool
		want   codes.Code
	}{
		{"empty caller", "", false, codes.PermissionDenied},
		{"public caller", "_public", true, codes.PermissionDenied},
		{"verified caller", "mod-a", true, codes.OK},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			if tc.setID {
				ctx = callerid.Set(ctx, tc.caller)
			}
			err := srv.checkVerifiedAuth(ctx)
			if tc.want == codes.OK {
				if err != nil {
					t.Fatalf("expected no error, got %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("expected error")
			}
			if status.Code(err) != tc.want {
				t.Errorf("expected %s, got %s", tc.want, status.Code(err))
			}
		})
	}
}

func TestAuthInterceptor_EventPublish_RequiresVerifiedIdentity(t *testing.T) {
	a := NewAuthInterceptor()
	a.SetAuthorizer(&stubAuthorizer{allow: true})
	a.SetIdentityProvider(&stubIdentityProvider{identity: nil})

	interceptor := a.UnaryInterceptor()
	ctx := metadata.NewOutgoingContext(context.Background(),
		metadata.Pairs("x-caller-id", "trusted-module"))

	_, err := interceptor(ctx, nil,
		fakeUnaryInfo("/muxcore.events.v1.EventService/Publish"),
		fakeHandler(nil, nil))
	if err == nil {
		t.Fatal("expected Publish to require verified identity")
	}
	st, _ := status.FromError(err)
	if st.Code() != codes.Unauthenticated {
		t.Errorf("expected Unauthenticated, got %s", st.Code())
	}
}

func TestEventServer_Publish_SpoofedCallerID_DeniedByPolicy(t *testing.T) {
	client, _ := startEventServerWithConfig(t, eventServerTestConfig{
		identityID:    "attacker-module",
		publishPolicy: allowCallerPublishPolicy{allowed: "trusted-module"},
		withAuth:      true,
	})

	_, err := client.Publish(spoofedCallerCtx("trusted-module"), &eventsv1.PublishRequest{
		Event: &eventsv1.Event{
			Type:   "sensitive.event",
			Source: "trusted-module",
		},
	})
	if err == nil {
		t.Fatal("expected publish denied when verified identity does not match policy")
	}
	if !strings.Contains(err.Error(), "publish denied") {
		t.Errorf("expected publish denied error, got: %v", err)
	}
}

func TestEventServer_Publish_SpoofedEventSource_DeniedByPolicy(t *testing.T) {
	client, _ := startEventServerWithConfig(t, eventServerTestConfig{
		identityID:    "attacker-module",
		publishPolicy: allowCallerPublishPolicy{allowed: "trusted-module"},
		withAuth:      true,
	})

	_, err := client.Publish(testAuthCtx(), &eventsv1.PublishRequest{
		Event: &eventsv1.Event{
			Type:   "sensitive.event",
			Source: "trusted-module",
		},
	})
	if err == nil {
		t.Fatal("expected publish denied when event.Source does not override verified identity")
	}
	if !strings.Contains(err.Error(), "publish denied") {
		t.Errorf("expected publish denied error, got: %v", err)
	}
}

func TestEventServer_Publish_UnauthenticatedDenied(t *testing.T) {
	client, _ := startEventServerWithConfig(t, eventServerTestConfig{
		identityID:    "",
		publishPolicy: allowAllPublishPolicy{},
		withAuth:      true,
	})

	_, err := client.Publish(testAuthCtx(), &eventsv1.PublishRequest{
		Event: &eventsv1.Event{Type: "test.event", Source: "any"},
	})
	if err == nil {
		t.Fatal("expected unauthenticated Publish to be denied")
	}
	st, _ := status.FromError(err)
	if st.Code() != codes.Unauthenticated {
		t.Errorf("expected Unauthenticated, got %s", st.Code())
	}
}

func TestEventServer_Publish_VerifiedIdentityAllowed(t *testing.T) {
	client, bus := startEventServerWithConfig(t, eventServerTestConfig{
		identityID:    "trusted-module",
		publishPolicy: allowCallerPublishPolicy{allowed: "trusted-module"},
		withAuth:      true,
	})

	received := make(chan contracts.Event, 1)
	bus.Subscribe(context.Background(), "allowed.event", func(ctx context.Context, e contracts.Event) error {
		received <- e
		return nil
	})

	_, err := client.Publish(testAuthCtx(), &eventsv1.PublishRequest{
		Event: &eventsv1.Event{
			Type:   "allowed.event",
			Source: "trusted-module",
		},
	})
	if err != nil {
		t.Fatalf("expected allowed publish, got: %v", err)
	}

	select {
	case ev := <-received:
		if ev.Type != "allowed.event" {
			t.Errorf("unexpected event type %q", ev.Type)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for allowed event")
	}
}
