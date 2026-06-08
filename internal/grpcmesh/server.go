package grpcmesh

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"sync"

	"github.com/Muxcore-Media/core/pkg/contracts"
	meshv1 "github.com/Muxcore-Media/core/proto/gen/muxcore/mesh/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/status"
)

// ErrRemoteRoutingUnavailable is returned when a cross-node call is attempted
// but no cluster module has been configured for remote routing.
var ErrRemoteRoutingUnavailable = errors.New("cross-node routing unavailable: no cluster module configured")

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
	server  *Server
	mu      sync.RWMutex
	cluster contracts.Cluster // optional; when set, cross-node routing becomes available
}

// NewClient creates a mesh client backed by the given server.
func NewClient(srv *Server) *Client {
	return &Client{
		server: srv,
	}
}

// SetCluster attaches a cluster module for cross-node routing.
func (c *Client) SetCluster(cluster contracts.Cluster) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cluster = cluster
}

// Call dispatches a call to the target module.
// Local modules are called in-process. Remote modules go over gRPC.
func (c *Client) Call(ctx context.Context, targetModule, method string, payload []byte) ([]byte, error) {
	// Try local first
	if result, err := c.server.localCall(ctx, targetModule, method, payload); err == nil {
		return result, nil
	}

	c.mu.RLock()
	cluster := c.cluster
	c.mu.RUnlock()

	if cluster != nil {
		// Check if the target module exists on any remote node
		for _, member := range cluster.Members() {
			for _, modID := range member.ModuleIDs {
				if modID == targetModule {
					return nil, fmt.Errorf("%w: module %q is available on node %q but cross-node gRPC routing is not yet implemented", ErrRemoteRoutingUnavailable, targetModule, member.ID)
				}
			}
		}
		return nil, fmt.Errorf("%w: module %q is not registered on any known node", ErrRemoteRoutingUnavailable, targetModule)
	}

	return nil, fmt.Errorf("%w: module %q not found locally and no cluster module is configured for remote routing", ErrRemoteRoutingUnavailable, targetModule)
}

// RegisterHandler registers a local module to receive calls.
func (c *Client) RegisterHandler(moduleID string, handler contracts.MeshHandler) {
	c.server.RegisterHandler(moduleID, handler)
}

// GRPCTransportCredentials creates transport credentials for the gRPC server
// from certificate and key files. If cert and key are both empty, returns
// (nil, nil) to signal that no TLS is configured and the caller should fall
// back to insecure mode.
//
// Environment variables:
//
//	MUXCORE_GRPC_TLS_CERT  — path to TLS certificate PEM file
//	MUXCORE_GRPC_TLS_KEY   — path to TLS private key PEM file
//	MUXCORE_GRPC_MTLS_ENABLED — if "true" or "1", enable mutual TLS
func GRPCTransportCredentials(certFile, keyFile string, mtlsEnabled bool) (credentials.TransportCredentials, error) {
	if certFile == "" && keyFile == "" {
		return nil, nil
	}

	// At least one of cert/key was specified; require both.
	if certFile == "" {
		return nil, fmt.Errorf("MUXCORE_GRPC_TLS_KEY is set but MUXCORE_GRPC_TLS_CERT is empty — both must be provided")
	}
	if keyFile == "" {
		return nil, fmt.Errorf("MUXCORE_GRPC_TLS_CERT is set but MUXCORE_GRPC_TLS_KEY is empty — both must be provided")
	}

	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return nil, fmt.Errorf("load TLS cert/key: %w", err)
	}

	tlsCfg := &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS12,
	}

	if mtlsEnabled {
		tlsCfg.ClientAuth = tls.RequireAndVerifyClientCert
		// In a full mTLS deployment the caller should populate ClientCAs
		// via an additional config field; for now this at least enforces
		// that clients present *some* certificate.
	}

	return credentials.NewTLS(tlsCfg), nil
}
