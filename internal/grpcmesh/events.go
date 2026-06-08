package grpcmesh

import (
	"context"

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
	event := contracts.Event{
		ID:        pb.GetId(),
		Type:      pb.GetType(),
		Source:    pb.GetSource(),
		Payload:   pb.GetPayload(),
		Metadata:  pb.GetMetadata(),
	}

	if err := s.bus.Publish(ctx, event); err != nil {
		return nil, err
	}
	return &eventsv1.PublishResponse{}, nil
}

// Subscribe streams events matching the given types from the remote node.
func (s *EventServer) Subscribe(req *eventsv1.SubscribeRequest, stream eventsv1.EventService_SubscribeServer) error {
	ctx := stream.Context()

	for _, eventType := range req.GetEventTypes() {
		eventType := eventType // capture
		if err := s.bus.Subscribe(ctx, eventType, func(ctx context.Context, event contracts.Event) error {
			pb := &eventsv1.Event{
				Id:          event.ID,
				Type:        event.Type,
				Source:      event.Source,
				Payload:     event.Payload,
				Metadata:    event.Metadata,
				Timestamp:   event.Timestamp.Unix(),
			}
			return stream.Send(pb)
		}); err != nil {
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
	event := contracts.Event{
		ID:        pb.GetId(),
		Type:      pb.GetType(),
		Source:    pb.GetSource(),
		Payload:   pb.GetPayload(),
		Metadata:  pb.GetMetadata(),
	}

	reply, err := s.bus.Request(ctx, event, 0)
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
