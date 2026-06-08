package grpcmesh

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"

	"github.com/Muxcore-Media/core/internal/callerid"
	"github.com/Muxcore-Media/core/pkg/contracts"
	storagev1 "github.com/Muxcore-Media/core/proto/gen/muxcore/storage/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// StorageServer implements the StorageService gRPC service, wrapping
// core's StorageOrchestrator so sidecar modules can access storage
// without in-process Fabric access.
//
// When a CallPolicyProvider is configured via SetCallPolicy(), every
// storage operation checks whether the caller module has the \"storage\"
// capability. Without a policy, all access is denied (deny-by-default).
type StorageServer struct {
	storagev1.UnimplementedStorageServiceServer
	store      contracts.StorageOrchestrator
	callPolicy contracts.CallPolicyProvider
}

// NewStorageServer creates a StorageServer backed by the given orchestrator.
func NewStorageServer(store contracts.StorageOrchestrator) *StorageServer {
	return &StorageServer{store: store}
}

// SetCallPolicy attaches a call policy provider for capability enforcement.
// Without a policy, all storage access is denied. Deploy a module implementing
// "call.policy" to grant access.
func (s *StorageServer) SetCallPolicy(cp contracts.CallPolicyProvider) {
	s.callPolicy = cp
}

// RegisterWithGRPC registers this server with a gRPC server.
func (s *StorageServer) RegisterWithGRPC(srv *grpc.Server) {
	storagev1.RegisterStorageServiceServer(srv, s)
}

// checkStorageAccess verifies that the caller is authorized to perform
// storage operations. Returns nil if access is granted.
func (s *StorageServer) checkStorageAccess(ctx context.Context, method string) error {
	if s.callPolicy == nil {
		return status.Error(codes.PermissionDenied, "storage access denied: no call policy configured")
	}
	callerID := callerid.Get(ctx)
	allowed, err := s.callPolicy.AllowCall(ctx, callerID, "storage", method)
	if err != nil {
		return status.Errorf(codes.Internal, "storage policy error: %v", err)
	}
	if !allowed {
		return status.Errorf(codes.PermissionDenied, "storage access denied: caller %q lacks storage capability", callerID)
	}
	return nil
}

// Put receives a client-streamed object and stores it.
func (s *StorageServer) Put(stream storagev1.StorageService_PutServer) error {
	if err := s.checkStorageAccess(stream.Context(), "write"); err != nil {
		return err
	}

	var key string
	var totalSize int64
	var wrote int64

	// Read first chunk synchronously — key and totalSize must be
	// extracted before we launch the pipe writer goroutine to avoid
	// a data race on those variables.
	firstReq, err := stream.Recv()
	if err == io.EOF {
		return status.Error(codes.InvalidArgument, "key is required in first PutRequest")
	}
	if err != nil {
		return status.Errorf(codes.Internal, "receive first chunk: %v", err)
	}
	key = firstReq.Key
	if key == "" {
		return status.Error(codes.InvalidArgument, "key is required in first PutRequest")
	}
	if len(key) > 1024 {
		return status.Errorf(codes.InvalidArgument, "key too long (%d bytes)", len(key))
	}
	if strings.Contains(key, "..") {
		return status.Error(codes.InvalidArgument, "key must not contain '..'")
	}
	totalSize = firstReq.TotalSize

	const maxChunkSize = 64 << 20 // 64 MB per chunk

	pr, pw := io.Pipe()

	// Write first chunk and remaining chunks asynchronously.
	errCh := make(chan error, 1)
	go func() {
		defer pw.Close()
		// Write first chunk
		chunk := firstReq.Chunk
		if len(chunk) > maxChunkSize {
			pw.CloseWithError(status.Errorf(codes.InvalidArgument, "chunk exceeds maximum size %d bytes", maxChunkSize))
			errCh <- fmt.Errorf("chunk too large: %d bytes", len(chunk))
			return
		}
		wrote += int64(len(chunk))
		if _, werr := pw.Write(chunk); werr != nil {
			pw.CloseWithError(werr)
			errCh <- werr
			return
		}
		// Write remaining chunks
		for {
			req, recvErr := stream.Recv()
			if recvErr == io.EOF {
				break
			}
			if recvErr != nil {
				pw.CloseWithError(recvErr)
				errCh <- recvErr
				return
			}
			if len(req.Chunk) > maxChunkSize {
				pw.CloseWithError(status.Errorf(codes.InvalidArgument, "chunk exceeds maximum size %d bytes", maxChunkSize))
				errCh <- fmt.Errorf("chunk too large: %d bytes", len(req.Chunk))
				return
			}
			wrote += int64(len(req.Chunk))
			if _, werr := pw.Write(req.Chunk); werr != nil {
				pw.CloseWithError(werr)
				errCh <- werr
				return
			}
		}
		errCh <- nil
	}()

	// Store via orchestrator.
	storeErr := s.store.Put(stream.Context(), key, pr, totalSize)
	pr.Close()
	if storeErr != nil {
		return status.Errorf(codes.Internal, "store put: %v", storeErr)
	}

	// Wait for writer goroutine to finish to ensure `wrote` is accurate.
	if werr := <-errCh; werr != nil {
		slog.Warn("storage put: writer error", "key", key, "error", werr)
	}

	return stream.SendAndClose(&storagev1.PutResponse{
		Key:  key,
		Size: wrote,
	})
}

// Get streams an object from storage to the client.
func (s *StorageServer) Get(req *storagev1.GetRequest, stream storagev1.StorageService_GetServer) error {
	if err := s.checkStorageAccess(stream.Context(), "read"); err != nil {
		return err
	}

	key := req.Key
	if key == "" {
		return status.Error(codes.InvalidArgument, "key is required")
	}
	if len(key) > 1024 {
		return status.Errorf(codes.InvalidArgument, "key too long (%d bytes)", len(key))
	}
	if strings.Contains(key, "..") {
		return status.Error(codes.InvalidArgument, "key must not contain '..'")
	}

	var reader io.ReadCloser
	var err error

	if req.Offset > 0 || req.Length > 0 {
		reader, err = s.store.Stream(stream.Context(), key, req.Offset, req.Length)
	} else {
		reader, err = s.store.Get(stream.Context(), key)
	}

	if err != nil {
		return status.Errorf(codes.NotFound, "get %q: %v", key, err)
	}
	defer reader.Close()

	// Stat to get total size for the first chunk.
	info, statErr := s.store.Stat(stream.Context(), key)
	totalSize := int64(0)
	contentType := ""
	if statErr == nil {
		totalSize = info.Size
		contentType = info.ContentType
	}

	buf := make([]byte, 64*1024) // 64KB chunks
	first := true
	for {
		n, readErr := reader.Read(buf)
		if n > 0 {
			resp := &storagev1.GetResponse{
				Chunk:       buf[:n],
				ContentType: contentType,
			}
			if first {
				resp.TotalSize = totalSize
				first = false
			}
			if err := stream.Send(resp); err != nil {
				return err
			}
		}
		if readErr != nil {
			if readErr == io.EOF {
				return nil
			}
			return status.Errorf(codes.Internal, "read %q: %v", key, readErr)
		}
	}
}

func (s *StorageServer) Delete(ctx context.Context, req *storagev1.DeleteRequest) (*storagev1.DeleteResponse, error) {
	if err := s.checkStorageAccess(ctx, "write"); err != nil {
		return nil, err
	}
	if req.Key == "" {
		return nil, status.Error(codes.InvalidArgument, "key is required")
	}
	if len(req.Key) > 1024 {
		return nil, status.Errorf(codes.InvalidArgument, "key too long (%d bytes)", len(req.Key))
	}
	if strings.Contains(req.Key, "..") {
		return nil, status.Error(codes.InvalidArgument, "key must not contain '..'")
	}
	err := s.store.Delete(ctx, req.Key)
	if err != nil {
		if errors.Is(err, contracts.ErrNotFound) {
			return &storagev1.DeleteResponse{Deleted: false}, nil
		}
		return nil, status.Errorf(codes.Internal, "delete %q: %v", req.Key, err)
	}
	return &storagev1.DeleteResponse{Deleted: true}, nil
}

func (s *StorageServer) Stat(ctx context.Context, req *storagev1.StatRequest) (*storagev1.StatResponse, error) {
	if err := s.checkStorageAccess(ctx, "read"); err != nil {
		return nil, err
	}
	if req.Key == "" {
		return nil, status.Error(codes.InvalidArgument, "key is required")
	}
	exists, err := s.store.Exists(ctx, req.Key)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "exists %q: %v", req.Key, err)
	}
	if !exists {
		return &storagev1.StatResponse{Found: false}, nil
	}
	info, err := s.store.Stat(ctx, req.Key)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "stat %q: %v", req.Key, err)
	}
	return &storagev1.StatResponse{
		Found:        true,
		Key:          info.Key,
		Size:         info.Size,
		ContentType:  info.ContentType,
		LastModified: info.LastModified.Unix(),
	}, nil
}

func (s *StorageServer) List(ctx context.Context, req *storagev1.ListRequest) (*storagev1.ListResponse, error) {
	if err := s.checkStorageAccess(ctx, "read"); err != nil {
		return nil, err
	}
	objects, err := s.store.List(ctx, req.Prefix)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "list: %v", err)
	}
	results := make([]*storagev1.StatResponse, 0, len(objects))
	for _, obj := range objects {
		results = append(results, &storagev1.StatResponse{
			Found:        true,
			Key:          obj.Key,
			Size:         obj.Size,
			ContentType:  obj.ContentType,
			LastModified: obj.LastModified.Unix(),
		})
	}
	return &storagev1.ListResponse{Objects: results}, nil
}

func (s *StorageServer) Capabilities(ctx context.Context, req *storagev1.CapabilitiesRequest) (*storagev1.CapabilitiesResponse, error) {
	// Check a well-known key to discover capabilities.
	caps, err := s.store.CapabilityCheck(ctx, "")
	if err != nil {
		// If CapabilityCheck fails, return empty capabilities.
		return &storagev1.CapabilitiesResponse{}, nil
	}
	if caps == nil {
		caps = []string{}
	}
	
	// Normalize capability names: "hardlinkable" from contracts matches
	// the proto convention. The contracts use CamelCase interface names;
	// we lower-case them for sidecar consumption.
	normalized := make([]string, 0, len(caps))
	for _, c := range caps {
		normalized = append(normalized, toSnakeCase(c))
	}
	return &storagev1.CapabilitiesResponse{Capabilities: normalized}, nil
}

// toSnakeCase converts CamelCase to snake_case for proto compatibility.
func toSnakeCase(s string) string {
	var result []byte
	for i, c := range s {
		if c >= 'A' && c <= 'Z' {
			if i > 0 {
				result = append(result, '_')
			}
			result = append(result, byte(c+32))
		} else {
			result = append(result, byte(c))
		}
	}
	return string(result)
}

// Ensure interface compliance.
var _ storagev1.StorageServiceServer = (*StorageServer)(nil)
