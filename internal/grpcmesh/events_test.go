package grpcmesh

import (
	"context"
	"io"
	"net"
	"testing"
	"time"

	"github.com/Muxcore-Media/core/internal/events"
	"github.com/Muxcore-Media/core/internal/registry"
	"github.com/Muxcore-Media/core/pkg/contracts"
	eventsv1 "github.com/Muxcore-Media/core/proto/gen/muxcore/events/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func startEventServer(t *testing.T) (eventsv1.EventServiceClient, *events.MemoryBus) {
	t.Helper()

	reg := registry.New()
	bus := events.NewMemoryBus()

	srv := NewEventServer(bus)
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	grpcSrv := grpc.NewServer()
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

func TestEventServer_Publish_Relay(t *testing.T) {
	client, bus := startEventServer(t)

	received := make(chan contracts.Event, 1)
	bus.Subscribe(context.Background(), "test.relayed", func(ctx context.Context, e contracts.Event) error {
		received <- e
		return nil
	})

	_, err := client.Publish(context.Background(), &eventsv1.PublishRequest{
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

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
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

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
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
		if err != nil && err != io.EOF {
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

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
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
	_, err := client.Publish(context.Background(), &eventsv1.PublishRequest{
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
