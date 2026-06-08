package grpcmesh

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/Muxcore-Media/core/pkg/contracts"
	meshv1 "github.com/Muxcore-Media/core/proto/gen/muxcore/mesh/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/metadata"
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

	// audit is an optional audit logger for recording gRPC calls.
	audit contracts.AuditLogger
	// nodeID identifies this node in audit entries.
	nodeID string
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

// SetAuditLogger attaches an audit logger for recording gRPC calls.
func (s *Server) SetAuditLogger(a contracts.AuditLogger) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.audit = a
}

// SetNodeID sets the node identifier for audit entries.
func (s *Server) SetNodeID(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nodeID = id
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

	// Audit the gRPC call if an audit logger is configured.
	s.mu.RLock()
	auditLogger := s.audit
	nodeID := s.nodeID
	s.mu.RUnlock()
	if auditLogger != nil {
		// Extract peer identity for the audit record.
		sourceAddr := "unknown"
		if p, ok := peer.FromContext(ctx); ok {
			sourceAddr = p.Addr.String()
		}
		entry := contracts.AuditEntry{
			ID:        uuid.New().String(),
			Timestamp: time.Now(),
			Actor:     sourceAddr,
			Action:    "grpc.call",
			Resource:  target,
			Details: map[string]string{
				"method": method,
			},
			NodeID: nodeID,
		}
		if err := auditLogger.Log(ctx, entry); err != nil {
			slog.Error("audit log write failed", "method", method, "error", err)
		}
	}

	result, err := handler.HandleCall(ctx, method, req.GetPayload())
	if err != nil {
		return &meshv1.CallResponse{Error: err.Error()}, nil
	}

	return &meshv1.CallResponse{Payload: result}, nil
}

// StreamCall handles bidirectional streaming of module calls.
// Each incoming CallRequest is dispatched to the target module's handler,
// and the result is streamed back as a CallResponse. This allows long-running
// or multi-message interactions over a single gRPC stream.
func (s *Server) StreamCall(stream meshv1.ModuleMesh_StreamCallServer) error {
	for {
		req, err := stream.Recv()
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}

		target := req.GetTargetModule()
		method := req.GetMethod()

		s.mu.RLock()
		handler, ok := s.handlers[target]
		s.mu.RUnlock()

		if !ok {
			return status.Errorf(codes.NotFound, "module %q not found on this node", target)
		}

		// Audit the gRPC streaming call if an audit logger is configured.
		s.mu.RLock()
		auditLogger := s.audit
		nodeID := s.nodeID
		s.mu.RUnlock()
		if auditLogger != nil {
			sourceAddr := "unknown"
			if p, ok := peer.FromContext(stream.Context()); ok {
				sourceAddr = p.Addr.String()
			}
			entry := contracts.AuditEntry{
				ID:        uuid.New().String(),
				Timestamp: time.Now(),
				Actor:     sourceAddr,
				Action:    "grpc.stream_call",
				Resource:  target,
				Details: map[string]string{
					"method": method,
				},
				NodeID: nodeID,
			}
			if err := auditLogger.Log(stream.Context(), entry); err != nil {
				slog.Error("audit log write failed", "error", err)
			}
		}

		result, err := handler.HandleCall(stream.Context(), method, req.GetPayload())
		if err != nil {
			if sendErr := stream.Send(&meshv1.CallResponse{Error: err.Error()}); sendErr != nil {
				return sendErr
			}
			continue
		}

		if sendErr := stream.Send(&meshv1.CallResponse{Payload: result}); sendErr != nil {
			return sendErr
		}
	}
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
	cluster    contracts.Cluster            // optional; when set, cross-node routing becomes available
	callPolicy contracts.CallPolicyProvider // optional; when set, call access control is enforced
	// transportCreds are the TLS credentials used for cross-node gRPC connections.
	// When nil (no TLS configured), cross-node routing is disabled rather than
	// falling back to insecure plaintext.
	transportCreds credentials.TransportCredentials
	// audit is an optional audit logger for recording outbound mesh calls.
	audit  contracts.AuditLogger
	// nodeID identifies this node in audit entries.
	nodeID string
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

// SetCallPolicy attaches a call policy module for inter-module access control.
// When set, every Call() checks with the policy provider before dispatching.
func (c *Client) SetCallPolicy(policy contracts.CallPolicyProvider) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.callPolicy = policy
}

// SetTransportCredentials sets the TLS credentials used for cross-node gRPC connections.
// When nil, cross-node routing is disabled — the mesh will only route to local modules.
// This prevents accidental plaintext fallback in production.
func (c *Client) SetTransportCredentials(creds credentials.TransportCredentials) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.transportCreds = creds
}

// SetAuditLogger attaches an audit logger for recording outbound mesh calls.
func (c *Client) SetAuditLogger(a contracts.AuditLogger) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.audit = a
}

// SetNodeID sets the node identifier for audit entries.
func (c *Client) SetNodeID(id string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.nodeID = id
}

// Call dispatches a call to the target module.
// Local modules are called in-process. Remote modules go over gRPC.
func (c *Client) Call(ctx context.Context, targetModule, method string, payload []byte) ([]byte, error) {
	// Check call policy if configured
	c.mu.RLock()
	callPolicy := c.callPolicy
	c.mu.RUnlock()
	if callPolicy != nil {
		callerID := contracts.CallerIDFromContext(ctx)
		allowed, err := callPolicy.AllowCall(ctx, callerID, targetModule, method)
		if err != nil {
			return nil, fmt.Errorf("call policy error: %w", err)
		}
		if !allowed {
			return nil, fmt.Errorf("call denied by policy: caller=%q target=%q method=%q", callerID, targetModule, method)
		}
	}

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
					// Build gRPC connection to the remote node
					// Require TLS for cross-node routing. Never fall back to insecure plaintext.
				if c.transportCreds == nil {
					return nil, fmt.Errorf("%w: cross-node routing requires TLS — no transport credentials configured", ErrRemoteRoutingUnavailable)
				}
				conn, err := grpc.Dial(member.GRPCAddr, grpc.WithTransportCredentials(c.transportCreds))
					if err != nil {
						return nil, fmt.Errorf("%w: failed to dial remote node %q at %s: %w", ErrRemoteRoutingUnavailable, member.ID, member.GRPCAddr, err)
					}
					defer conn.Close()

					// Propagate caller identity via gRPC metadata if set
					callCtx := ctx
					if callerID := contracts.CallerIDFromContext(ctx); callerID != "" {
						callCtx = metadata.NewOutgoingContext(ctx, metadata.Pairs("x-caller-id", callerID))
					}

					// Build and send the remote call request
					client := meshv1.NewModuleMeshClient(conn)
					req := &meshv1.CallRequest{
						TargetModule: targetModule,
						Method:       method,
						Payload:      payload,
					}

					resp, err := client.Call(callCtx, req)
					if err != nil {
						return nil, fmt.Errorf("%w: remote call to node %q failed: %w", ErrRemoteRoutingUnavailable, member.ID, err)
					}
					if resp.Error != "" {
						return nil, fmt.Errorf("remote call error from module %q on node %q: %s", targetModule, member.ID, resp.Error)
					}
					return resp.Payload, nil
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
// (nil, nil) only when MUXCORE_INSECURE_DISABLE_TLS is set to true or 1.
//
// Environment variables:
//
//	MUXCORE_GRPC_TLS_CERT  — path to TLS certificate PEM file
//	MUXCORE_GRPC_TLS_KEY   — path to TLS private key PEM file
//	MUXCORE_GRPC_MTLS_ENABLED — if "true" or "1", enable mutual TLS
func GRPCTransportCredentials(certFile, keyFile, caCertFile string, mtlsEnabled bool) (credentials.TransportCredentials, error) {
	if certFile == "" && keyFile == "" {
		if os.Getenv("MUXCORE_INSECURE_DISABLE_TLS") == "true" || os.Getenv("MUXCORE_INSECURE_DISABLE_TLS") == "1" {
			return nil, nil // insecure mode explicitly enabled
		}
		return nil, fmt.Errorf("TLS is required for gRPC — set MUXCORE_GRPC_TLS_CERT and MUXCORE_GRPC_TLS_KEY to enable encryption")
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
		// Restrict cipher suites to strong AEAD ciphers (no CBC, no RC4, no 3DES).
		// Go's TLS 1.3 cipher suites are always safe (only AEAD), so this list
		// applies to TLS 1.2 handshakes only.
		CipherSuites: []uint16{
			tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,
			tls.TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384,
			tls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256,
			tls.TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384,
		},
	}

	if mtlsEnabled {
		if caCertFile == "" {
			return nil, fmt.Errorf("mTLS is enabled but no CA certificate file was provided — set MUXCORE_GRPC_MTLS_CA")
		}
		caCert, err := os.ReadFile(caCertFile)
		if err != nil {
			return nil, fmt.Errorf("read mTLS CA cert: %w", err)
		}
		caCertPool := x509.NewCertPool()
		if !caCertPool.AppendCertsFromPEM(caCert) {
			return nil, fmt.Errorf("failed to parse CA certificate from %q", caCertFile)
		}
		tlsCfg.ClientAuth = tls.RequireAndVerifyClientCert
		tlsCfg.ClientCAs = caCertPool
	}

	return credentials.NewTLS(tlsCfg), nil
}
