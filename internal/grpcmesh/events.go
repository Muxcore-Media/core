package grpcmesh

import (
	"context"
	"time"

	"github.com/Muxcore-Media/core/pkg/contracts"
	eventsv1 "github.com/Muxcore-Media/core/proto/gen/muxcore/events/v1"
	"google.golang.org/grpc"
)

// EventServer implements the EventService gRPC service.
// It relays events between nodes when NATS is not in use.
type EventServer struct {
	eventsv1.UnimplementedEventServiceServer
	bus contracts.EventBus
}

// NewEventServer creates an event relay gRPC server.
func NewEventServer(bus contracts.EventBus) *EventServer {
	return &EventServer{bus: bus}
}

// RegisterWithGRPC registers this server with a gRPC server.
func (s *EventServer) RegisterWithGRPC(srv *grpc.Server) {
	eventsv1.RegisterEventServiceServer(srv, s)
}

// Publish receives an event from a remote node and publishes it locally.
func (s *EventServer) Publish(ctx context.Context, req *eventsv1.PublishRequest) (*eventsv1.PublishResponse, error) {
	pb := req.GetEvent()

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
			// Use background context so unsubscription is not cancelled
			// by the already-cancelled stream context.
			_ = s.bus.Unsubscribe(context.Background(), sub.eventType, sub.handler)
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

	reply, err := s.bus.Request(ctx, event, 30*time.Second)
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
