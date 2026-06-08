package grpcmesh

import (
	"context"
	"time"

	"github.com/Muxcore-Media/core/pkg/contracts"
	eventsv1 "github.com/Muxcore-Media/core/proto/gen/muxcore/events/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
)

// EventServer implements the EventService gRPC service.
// It relays events between nodes when NATS is not in use.
type EventServer struct {
	eventsv1.UnimplementedEventServiceServer
	bus         contracts.EventBus
	requireAuth bool
}

// NewEventServer creates an event relay gRPC server.
func NewEventServer(bus contracts.EventBus) *EventServer {
	return &EventServer{bus: bus}
}

// SetRequireAuth controls whether Subscribe and Publish require an
// authenticated caller (non-empty x-caller-id in context). When true,
// unauthenticated requests receive codes.Unauthenticated.
func (s *EventServer) SetRequireAuth(require bool) {
	s.requireAuth = require
}

func (s *EventServer) checkAuth(ctx context.Context) error {
	if !s.requireAuth {
		return nil
	}
	if contracts.CallerIDFromContext(ctx) == "" {
		return status.Error(codes.Unauthenticated,
			"events: authentication required — set MUXCORE_GRPC_REQUIRE_EVENTS_AUTH=false or deploy an auth module")
	}
	return nil
}

// RegisterWithGRPC registers this server with a gRPC server.
func (s *EventServer) RegisterWithGRPC(srv *grpc.Server) {
	eventsv1.RegisterEventServiceServer(srv, s)
}

const maxEventPayloadSize = 10 << 20 // 10 MB

// Publish receives an event from a remote node and publishes it locally.
func (s *EventServer) Publish(ctx context.Context, req *eventsv1.PublishRequest) (*eventsv1.PublishResponse, error) {
	if err := s.checkAuth(ctx); err != nil {
		return nil, err
	}
	pb := req.GetEvent()
	if len(pb.GetPayload()) > maxEventPayloadSize {
		return nil, status.Errorf(codes.InvalidArgument, "event payload exceeds maximum size %d bytes", maxEventPayloadSize)
	}

	// Override source_node from authenticated peer to prevent source spoofing.
	if p, ok := peer.FromContext(ctx); ok {
		if addr := p.Addr.String(); addr != "" {
			pb.SourceNode = addr
		}
	}

	// Preserve proto fields that contracts.Event does not have as top-level fields.
	metadata := pb.GetMetadata()
	if metadata == nil {
		metadata = make(map[string]string)
	}
	if sourceNode := pb.GetSourceNode(); sourceNode != "" {
		metadata["source_node"] = sourceNode
	}

	event := contracts.Event{
		ID:        pb.GetId(),
		Type:      pb.GetType(),
		Source:    pb.GetSource(),
		Payload:   pb.GetPayload(),
		Metadata:  metadata,
		Timestamp: time.Unix(pb.GetTimestamp(), 0),
	}

	if err := s.bus.Publish(ctx, event); err != nil {
		return nil, err
	}
	return &eventsv1.PublishResponse{}, nil
}

// Subscribe streams events matching the given types from the remote node.
func (s *EventServer) Subscribe(req *eventsv1.SubscribeRequest, stream eventsv1.EventService_SubscribeServer) error {
	if err := s.checkAuth(stream.Context()); err != nil {
		return err
	}
	ctx := stream.Context()

	// Track all subscriptions so we can clean them up when the stream ends.
	type subscription struct {
		eventType string
		handler   contracts.EventHandler
	}
	subs := make([]subscription, 0, len(req.GetEventTypes()))

	// Unsubscribe all tracked subscriptions on stream termination.
		defer func() {
		for _, sub := range subs {
			if err := s.bus.Unsubscribe(context.Background(), sub.eventType, sub.handler); err != nil {
				slog.Warn("events: unsubscribe failed during stream cleanup",
					"event_type", sub.eventType, "error", err)
			}
		}
	}()

	for _, eventType := range req.GetEventTypes() {
		eventType := eventType // capture
		handler := func(ctx context.Context, event contracts.Event) error {
			pb := &eventsv1.Event{
				Id:          event.ID,
				Type:        event.Type,
				Source:      event.Source,
				Payload:     event.Payload,
				Metadata:    event.Metadata,
				Timestamp:   event.Timestamp.Unix(),
			}
			return stream.Send(pb)
		}
		// Store before subscribing so the handler reference is captured.
		subs = append(subs, subscription{eventType: eventType, handler: handler})
		if err := s.bus.Subscribe(ctx, eventType, handler); err != nil {
			return err
		}
	}

	// Wait until stream is closed
	<-ctx.Done()
	return nil
}

// Request sends a request event and waits for a single reply.
func (s *EventServer) Request(ctx context.Context, req *eventsv1.RequestEvent) (*eventsv1.Event, error) {
	pb := req.GetEvent()

	metadata := pb.GetMetadata()
	if metadata == nil {
		metadata = make(map[string]string)
	}
	if sourceNode := pb.GetSourceNode(); sourceNode != "" {
		metadata["source_node"] = sourceNode
	}

	event := contracts.Event{
		ID:        pb.GetId(),
		Type:      pb.GetType(),
		Source:    pb.GetSource(),
		Payload:   pb.GetPayload(),
		Metadata:  metadata,
		Timestamp: time.Unix(pb.GetTimestamp(), 0),
	}

	timeout := 30 * time.Second
	if req.TimeoutMs > 0 {
		timeout = time.Duration(req.TimeoutMs) * time.Millisecond
	}
	reply, err := s.bus.Request(ctx, event, timeout)
	if err != nil {
		return nil, err
	}

	return &eventsv1.Event{
		Id:        reply.ID,
		Type:      reply.Type,
		Source:    reply.Source,
		Payload:   reply.Payload,
		Metadata:  reply.Metadata,
		Timestamp: reply.Timestamp.Unix(),
	}, nil
}
