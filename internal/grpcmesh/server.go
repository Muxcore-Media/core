package grpcmesh

import (
	"context"
	"fmt"
	"sync"

	"github.com/Muxcore-Media/core/pkg/contracts"
	meshv1 "github.com/Muxcore-Media/core/proto/gen/muxcore/mesh/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Server implements the ModuleMesh gRPC service and routes calls to local modules.
type Server struct {
	meshv1.UnimplementedModuleMeshServer

	mu       sync.RWMutex
	handlers map[string]contracts.MeshHandler // moduleID -> handler

	// Client provides remote call routing. Set after construction.
	client *Client
}

// NewServer creates a new mesh server.
func NewServer() *Server {
	return &Server{
		handlers: make(map[string]contracts.MeshHandler),
	}
}

// RegisterHandler registers a local module to receive Call requests.
func (s *Server) RegisterHandler(moduleID string, handler contracts.MeshHandler) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.handlers[moduleID] = handler
}

// SetClient sets the mesh client for remote call routing.
func (s *Server) SetClient(c *Client) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.client = c
}

// RegisterWithGRPC registers this server with a gRPC server.
func (s *Server) RegisterWithGRPC(srv *grpc.Server) {
	meshv1.RegisterModuleMeshServer(srv, s)
}

// Call handles an incoming gRPC Call request.
func (s *Server) Call(ctx context.Context, req *meshv1.CallRequest) (*meshv1.CallResponse, error) {
	target := req.GetTargetModule()
	method := req.GetMethod()

	s.mu.RLock()
	handler, ok := s.handlers[target]
	s.mu.RUnlock()

	if !ok {
		return nil, status.Errorf(codes.NotFound, "module %q not found on this node", target)
	}

	result, err := handler.HandleCall(ctx, method, req.GetPayload())
	if err != nil {
		return &meshv1.CallResponse{Error: err.Error()}, nil
	}

	return &meshv1.CallResponse{Payload: result}, nil
}

// StreamCall handles bidirectional streaming.
func (s *Server) StreamCall(stream meshv1.ModuleMesh_StreamCallServer) error {
	return status.Error(codes.Unimplemented, "StreamCall not yet implemented")
}

// localCall routes a call to a local handler directly — no network.
func (s *Server) localCall(ctx context.Context, targetModule, method string, payload []byte) ([]byte, error) {
	s.mu.RLock()
	handler, ok := s.handlers[targetModule]
	s.mu.RUnlock()

	if !ok {
		return nil, fmt.Errorf("module %q not found locally", targetModule)
	}

	return handler.HandleCall(ctx, method, payload)
}

// --- ModuleMeshClient implementation ---

// Client implements contracts.ModuleMeshClient.
// It routes calls: local modules get in-process dispatch; remote modules go over gRPC.
type Client struct {
	server     *Server
	mu         sync.RWMutex
	conns      map[string]*grpc.ClientConn // nodeAddr -> conn
	meshClient meshv1.ModuleMeshClient     // client to self (for same-node calls via gRPC)
}

// NewClient creates a mesh client backed by the given server.
func NewClient(srv *Server) *Client {
	return &Client{
		server: srv,
		conns:  make(map[string]*grpc.ClientConn),
	}
}

// Call dispatches a call to the target module.
// Local modules are called in-process. Remote modules go over gRPC.
func (c *Client) Call(ctx context.Context, targetModule, method string, payload []byte) ([]byte, error) {
	// Try local first
	if result, err := c.server.localCall(ctx, targetModule, method, payload); err == nil {
		return result, nil
	}

	// TODO: remote routing via Cluster module (Phase 4)
	return nil, fmt.Errorf("module %q not found locally and no cluster module available for remote routing", targetModule)
}

// RegisterHandler registers a local module to receive calls.
func (c *Client) RegisterHandler(moduleID string, handler contracts.MeshHandler) {
	c.server.RegisterHandler(moduleID, handler)
}
