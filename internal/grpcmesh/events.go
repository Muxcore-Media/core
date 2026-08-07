package grpcmesh

import (
	"context"
	"log/slog"
	"time"

	"github.com/Muxcore-Media/core/internal/callerid"
	"github.com/Muxcore-Media/core/pkg/contracts"
	eventsv1 "github.com/Muxcore-Media/core/proto/gen/muxcore/events/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
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
// Authentication is required for all operations.
type EventServer struct {
	eventsv1.UnimplementedEventServiceServer
	bus         contracts.EventBus
	walReplayer WALReplayer
}

// NewEventServer creates an event relay gRPC server.
// Authentication is required for all operations.
func NewEventServer(bus contracts.EventBus) *EventServer {
	return &EventServer{bus: bus}
}

// SetWALReplayer enables the Replay RPC backed by the given replayer.
// Call this only when MUXCORE_EVENT_JOURNAL_PATH is set.
func (s *EventServer) SetWALReplayer(r WALReplayer) {
	s.walReplayer = r
}

func (s *EventServer) checkAuth(ctx context.Context) error {
	id := callerid.Get(ctx)
	if id != "" {
		return nil
	}
	// Fallback: check gRPC metadata directly for caller identity.
	if md, ok := metadata.FromIncomingContext(ctx); ok {
		if ids := md.Get("x-caller-id"); len(ids) > 0 && ids[0] != "" {
			return nil
		}
	}
	return status.Error(codes.PermissionDenied,
		"events: authentication required")
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
	// Allowlist path stamps callerid "_public"; prefer module identity from
	// metadata / event source so publish-policy can authorize the real caller.
	if id := callerid.Get(ctx); id == "" || id == "_public" {
		if md, ok := metadata.FromIncomingContext(ctx); ok {
			if ids := md.Get("x-caller-id"); len(ids) > 0 && ids[0] != "" {
				ctx = callerid.Set(ctx, ids[0])
			}
		}
	}
	pb := req.GetEvent()
	if pb == nil {
		return nil, status.Error(codes.InvalidArgument, "event is required")
	}
	if id := callerid.Get(ctx); id == "" || id == "_public" {
		if pb.GetSource() != "" {
			ctx = callerid.Set(ctx, pb.GetSource())
		}
	}
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
	md := pb.GetMetadata()
	if md == nil {
		md = make(map[string]string)
	}
	if sourceNode := pb.GetSourceNode(); sourceNode != "" {
		md["source_node"] = sourceNode
	}

	event := contracts.Event{
		ID:        pb.GetId(),
		Type:      pb.GetType(),
		Source:    pb.GetSource(),
		Payload:   pb.GetPayload(),
		Metadata:  md,
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

	// Serialize all stream.Send calls through a single channel + goroutine.
	// gRPC streams require that Send be called by at most one goroutine at a
	// time; without serialization, concurrent sends from multiple bus
	// subscriptions race and trigger "transport: SendHeader called multiple times".
	type pbEvent struct {
		event contracts.Event
		done  chan error
	}
	ch := make(chan pbEvent, 64)

	go func() {
		defer func() {
			if r := recover(); r != nil {
				slog.Error("events subscribe send panic recovered", "panic", r)
			}
		}()
		for pe := range ch {
			pb := &eventsv1.Event{
				Id:        pe.event.ID,
				Type:      pe.event.Type,
				Source:    pe.event.Source,
				Payload:   pe.event.Payload,
				Metadata:  pe.event.Metadata,
				Timestamp: pe.event.Timestamp.Unix(),
			}
			select {
			case pe.done <- stream.Send(pb):
			case <-ctx.Done():
				return
			}
		}
	}()

	// Collect cancel funcs so all subscriptions are cleaned up when the stream ends.
	cancels := make([]func(), 0, len(req.GetEventTypes()))
	defer func() {
		for _, cancel := range cancels {
			cancel()
		}
		close(ch)
	}()

	for _, eventType := range req.GetEventTypes() {
		eventType := eventType // capture
		handler := func(ctx context.Context, event contracts.Event) error {
			done := make(chan error, 1)
			select {
			case ch <- pbEvent{event: event, done: done}:
			case <-ctx.Done():
				return ctx.Err()
			}
			select {
			case err := <-done:
				return err
			case <-ctx.Done():
				return ctx.Err()
			}
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

	md := pb.GetMetadata()
	if md == nil {
		md = make(map[string]string)
	}
	if sourceNode := pb.GetSourceNode(); sourceNode != "" {
		md["source_node"] = sourceNode
	}

	event := contracts.Event{
		ID:        pb.GetId(),
		Type:      pb.GetType(),
		Source:    pb.GetSource(),
		Payload:   pb.GetPayload(),
		Metadata:  md,
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

	sinceSeq := uint64(req.GetSinceSeq()) //nolint:gosec // safe: non-negative sequence numbers
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
