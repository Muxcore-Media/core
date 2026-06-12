package grpcmesh

import (
	"context"
	"io"
	"time"

	"github.com/Muxcore-Media/core/internal/callerid"
	"github.com/Muxcore-Media/core/pkg/contracts"
	auditv1 "github.com/Muxcore-Media/core/proto/gen/muxcore/audit/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// AuditServer implements the AuditService gRPC service for querying and
// exporting audit logs. Admin tools use this to browse and verify audit trails.
type AuditServer struct {
	auditv1.UnimplementedAuditServiceServer
	logger contracts.AuditLogger
}

// NewAuditServer creates a gRPC server for audit log access.
// Pass nil for logger to create a no-op instance (audit disabled).
func NewAuditServer(logger contracts.AuditLogger) *AuditServer {
	return &AuditServer{logger: logger}
}

// RegisterWithGRPC registers this server with a gRPC server.
func (s *AuditServer) RegisterWithGRPC(srv *grpc.Server) {
	auditv1.RegisterAuditServiceServer(srv, s)
}

// checkAuth enforces caller authentication for audit operations.
func (s *AuditServer) checkAuth(ctx context.Context) error {
	callerID := callerid.Get(ctx)
	if callerID == "" || callerID == "_public" {
		return status.Error(codes.PermissionDenied, "audit: authentication required")
	}
	return nil
}

// Query returns audit entries matching the given filter.
func (s *AuditServer) Query(ctx context.Context, req *auditv1.AuditQueryRequest) (*auditv1.AuditQueryResponse, error) {
	if err := s.checkAuth(ctx); err != nil {
		return nil, err
	}
	if s.logger == nil {
		return &auditv1.AuditQueryResponse{}, nil
	}

	filter := auditFilterFromProto(req)
	entries, err := s.logger.Query(ctx, filter)
	if err != nil {
		return nil, status.Errorf(codes.Unavailable, "audit query: %v", err)
	}

	protoEntries := make([]*auditv1.AuditEntryProto, len(entries))
	for i, e := range entries {
		protoEntries[i] = auditEntryToProto(e)
	}

	return &auditv1.AuditQueryResponse{
		Entries: protoEntries,
		Total:   int32(len(protoEntries)), //nolint:gosec // bounded by gRPC message size
	}, nil
}

// Export streams audit entries in the requested format.
func (s *AuditServer) Export(req *auditv1.AuditExportRequest, stream auditv1.AuditService_ExportServer) error {
	if err := s.checkAuth(stream.Context()); err != nil {
		return err
	}
	if s.logger == nil {
		return stream.Send(&auditv1.AuditExportChunk{Complete: true})
	}

	format := req.GetFormat()
	if format == "" {
		format = "json"
	}

	reader, err := s.logger.Export(stream.Context(), format)
	if err != nil {
		return status.Errorf(codes.Unavailable, "audit export: %v", err)
	}
	defer func() { _ = reader.Close() }()

	buf := make([]byte, 64*1024) // 64KB chunks
	seq := int32(0)

	for {
		n, readErr := reader.Read(buf)
		if n > 0 {
			chunk := make([]byte, n)
			copy(chunk, buf[:n])
			if err := stream.Send(&auditv1.AuditExportChunk{
				Data:     chunk,
				Sequence: seq,
				Complete: false,
			}); err != nil {
				return err
			}
			seq++
		}
		if readErr == io.EOF {
			return stream.Send(&auditv1.AuditExportChunk{
				Sequence: seq,
				Complete: true,
			})
		}
		if readErr != nil {
			return status.Errorf(codes.Unavailable, "audit export read: %v", readErr)
		}
	}
}

// VerifyChain performs a standalone hash-chain integrity check.
func (s *AuditServer) VerifyChain(ctx context.Context, req *auditv1.AuditVerifyChainRequest) (*auditv1.AuditVerifyChainResponse, error) {
	if err := s.checkAuth(ctx); err != nil {
		return nil, err
	}
	if s.logger == nil {
		return &auditv1.AuditVerifyChainResponse{Valid: true}, nil
	}

	from := time.Time{}
	to := time.Now()

	if req.GetFromTime() != "" {
		parsed, err := time.Parse(time.RFC3339, req.GetFromTime())
		if err != nil {
			return nil, status.Errorf(codes.InvalidArgument, "invalid from_time: %v", err)
		}
		from = parsed
	}
	if req.GetToTime() != "" {
		parsed, err := time.Parse(time.RFC3339, req.GetToTime())
		if err != nil {
			return nil, status.Errorf(codes.InvalidArgument, "invalid to_time: %v", err)
		}
		to = parsed
	}

	result, err := s.logger.VerifyChainIntegrity(ctx, from, to)
	if err != nil {
		return nil, status.Errorf(codes.Unavailable, "audit verify chain: %v", err)
	}

	firstBroken := ""
	if !result.FirstBrokenAt.IsZero() {
		firstBroken = result.FirstBrokenAt.Format(time.RFC3339)
	}

	return &auditv1.AuditVerifyChainResponse{
		Valid:         result.Valid,
		TotalEntries:  int32(result.TotalEntries), //nolint:gosec // bounded by gRPC message size
		BrokenLinks:   result.BrokenLinks,
		FirstBrokenAt: firstBroken,
	}, nil
}

func auditFilterFromProto(req *auditv1.AuditQueryRequest) contracts.AuditFilter {
	filter := contracts.AuditFilter{
		Actor:       req.GetActor(),
		Action:      req.GetAction(),
		Resource:    req.GetResource(),
		TraceID:     req.GetTraceId(),
		VerifyChain: req.GetVerifyChain(),
	}

	if req.GetFromTime() != "" {
		if t, err := time.Parse(time.RFC3339, req.GetFromTime()); err == nil {
			filter.From = t
		}
	}
	if req.GetToTime() != "" {
		if t, err := time.Parse(time.RFC3339, req.GetToTime()); err == nil {
			filter.To = t
		}
	}

	return filter
}

func auditEntryToProto(e contracts.AuditEntry) *auditv1.AuditEntryProto {
	timestamp := e.Timestamp.Unix()
	return &auditv1.AuditEntryProto{
		Id:            e.ID,
		Timestamp:     timestamp,
		Actor:         e.Actor,
		Action:        e.Action,
		Resource:      e.Resource,
		ResourceId:    e.ResourceID,
		Details:       e.Details,
		TraceId:       e.TraceID,
		NodeId:        e.NodeID,
		PrevEntryHash: e.PrevEntryHash,
		Signature:     e.Signature,
	}
}
