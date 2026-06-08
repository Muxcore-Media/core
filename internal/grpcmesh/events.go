package grpcmesh

import (
	"context"
	"time"

	"github.com/Muxcore-Media/core/internal/callerid"
	"github.com/Muxcore-Media/core/pkg/contracts"
	eventsv1 "github.com/Muxcore-Media/core/proto/gen/muxcore/events/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
)

// WALReplayer provides access to WAL event history for the Replay RPC.
// *events.MemoryBus satisfies this interface when a WAL is configured.
type WALReplayer interface {
	ReplayFrom(ctx context.Context, sinceSeq uint64, fn func(contracts.Event) error) error
}

// EventServer implements the EventService gRPC service.
// It relays events between nodes when NATS is not in use.
type EventServer struct {
	eventsv1.UnimplementedEventServiceServer
	bus         contracts.EventBus
	walReplayer WALReplayer
	requireAuth bool
}

// NewEventServer creates an event relay gRPC server.
func NewEventServer(bus contracts.EventBus) *EventServer {
	return &EventServer{bus: bus}
}

// SetWALReplayer enables the Replay RPC backed by the given replayer.
// Call this only when MUXCORE_EVENT_JOURNAL_PATH is set.
func (s *EventServer) SetWALReplayer(r WALReplayer) {
	s.walReplayer = r
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
	if callerid.Get(ctx) == "" {
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

	// Collect cancel funcs so all subscriptions are cleaned up when the stream ends.
	cancels := make([]func(), 0, len(req.GetEventTypes()))
	defer func() {
		for _, cancel := range cancels {
			cancel()
		}
	}()

	for _, eventType := range req.GetEventTypes() {
		eventType := eventType // capture
		handler := func(ctx context.Context, event contracts.Event) error {
			pb := &eventsv1.Event{
				Id:        event.ID,
				Type:      event.Type,
				Source:    event.Source,
				Payload:   event.Payload,
				Metadata:  event.Metadata,
				Timestamp: event.Timestamp.Unix(),
			}
			return stream.Send(pb)
		}
		cancel, err := s.bus.Subscribe(ctx, eventType, handler)
		if err != nil {
			return err
		}
		cancels = append(cancels, cancel)
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

// Replay streams historical events from the WAL starting at since_seq.
// Returns Unimplemented if no WAL replayer is configured on this node.
func (s *EventServer) Replay(req *eventsv1.ReplayRequest, stream eventsv1.EventService_ReplayServer) error {
	if err := s.checkAuth(stream.Context()); err != nil {
		return err
	}
	if s.walReplayer == nil {
		return status.Error(codes.Unimplemented,
			"WAL replay not available: set MUXCORE_EVENT_JOURNAL_PATH on this node to enable it")
	}

	sinceSeq := uint64(req.GetSinceSeq())
	typeFilter := req.GetEventType()

	return s.walReplayer.ReplayFrom(stream.Context(), sinceSeq, func(event contracts.Event) error {
		if typeFilter != "" && event.Type != typeFilter {
			return nil
		}
		return stream.Send(&eventsv1.Event{
			Id:        event.ID,
			Type:      event.Type,
			Source:    event.Source,
			Payload:   event.Payload,
			Metadata:  event.Metadata,
			Timestamp: event.Timestamp.Unix(),
		})
	})
}

