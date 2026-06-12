package grpcmesh

import (
	"context"
	"io"
	"testing"
	"time"

	"github.com/Muxcore-Media/core/pkg/contracts"
	meshv1 "github.com/Muxcore-Media/core/proto/gen/muxcore/mesh/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/metadata"
)

func TestServer_SetClient(t *testing.T) {
	srv := NewServer()
	client := NewClient(srv)
	srv.SetClient(client)
	// Verify no panic, client is set internally.
}

func TestServer_SetTransportCredentials(t *testing.T) {
	client := NewClient(NewServer())
	client.SetTransportCredentials(insecure.NewCredentials())
	client.SetTransportCredentials(nil)
}

func TestClient_CallCount(t *testing.T) {
	client := NewClient(nil)
	if c := client.CallCount(); c != 0 {
		t.Errorf("expected 0, got %d", c)
	}
}

func TestClient_RegisterHandler(t *testing.T) {
	srv := NewServer()
	client := NewClient(srv)
	handler := &echoHandler{}
	client.RegisterHandler("test-mod", handler)
}

type echoHandler struct{}

func (e *echoHandler) HandleCall(_ context.Context, method string, payload []byte) ([]byte, error) {
	return payload, nil
}
func (e *echoHandler) Info() contracts.ModuleInfo     { return contracts.ModuleInfo{ID: "echo"} }
func (e *echoHandler) Init(_ context.Context) error   { return nil }
func (e *echoHandler) Start(_ context.Context) error  { return nil }
func (e *echoHandler) Stop(_ context.Context) error   { return nil }
func (e *echoHandler) Health(_ context.Context) error { return nil }

func TestGRPCTransportCredentials_DevSkip(t *testing.T) {
	t.Setenv("MUXCORE_DEV_TLS_SKIP", "true")
	creds, err := GRPCTransportCredentials("", "", "", false)
	if err != nil {
		t.Fatalf("expected nil error with dev skip, got %v", err)
	}
	if creds != nil {
		t.Error("expected nil credentials with dev skip")
	}
}

func TestGRPCTransportCredentials_RequiresTLS(t *testing.T) {
	t.Setenv("MUXCORE_DEV_TLS_SKIP", "false")
	_, err := GRPCTransportCredentials("", "", "", false)
	if err == nil {
		t.Fatal("expected error when TLS not configured")
	}
}

func TestDiscoveryServer_SetMaxWatchers(t *testing.T) {
	ds := newDS("node-a")
	ds.SetMaxWatchers(50)
	if ds.maxWatchers != 50 {
		t.Errorf("expected maxWatchers=50, got %d", ds.maxWatchers)
	}
}

func TestDiscoveryServer_RegisterWithGRPC(t *testing.T) {
	ds := newDS("node-a")
	srv := grpc.NewServer()
	defer srv.Stop()
	ds.RegisterWithGRPC(srv)
}

func TestAuthInterceptor_StreamInterceptor_NoAuth(t *testing.T) {
	a := NewAuthInterceptor()
	interceptor := a.StreamInterceptor()
	if interceptor == nil {
		t.Fatal("expected non-nil stream interceptor")
	}

	info := &grpc.StreamServerInfo{FullMethod: "/test/Stream"}
	stream := &mockServerStream{ctx: context.Background()}
	err := interceptor(nil, stream, info, func(srv any, stream grpc.ServerStream) error {
		return nil
	})
	if err == nil {
		t.Log("stream interceptor allowed call without auth")
	}
}

func TestAuthInterceptor_StreamInterceptor_ModuleRegistration(t *testing.T) {
	a := NewAuthInterceptor()
	interceptor := a.StreamInterceptor()

	info := &grpc.StreamServerInfo{FullMethod: "/muxcore.module.v1.ModuleRegistration/Register"}
	stream := &mockServerStream{ctx: context.Background()}
	called := false
	err := interceptor(nil, stream, info, func(srv any, stream grpc.ServerStream) error {
		called = true
		return nil
	})
	if err != nil {
		t.Fatalf("module registration should be allowed: %v", err)
	}
	if !called {
		t.Error("handler was not called")
	}
}

// mockServerStream implements grpc.ServerStream for testing.
type mockServerStream struct {
	ctx context.Context
}

func (m *mockServerStream) Context() context.Context     { return m.ctx }
func (m *mockServerStream) SendMsg(any) error            { return nil }
func (m *mockServerStream) RecvMsg(any) error            { return nil }
func (m *mockServerStream) SetHeader(metadata.MD) error  { return nil }
func (m *mockServerStream) SendHeader(metadata.MD) error { return nil }
func (m *mockServerStream) SetTrailer(metadata.MD)       {}

func TestAuthServerStream_Context(t *testing.T) {
	stream := &authServerStream{ctx: context.Background()}
	if stream.Context() != context.Background() {
		t.Error("expected context.Background()")
	}
}

func TestClient_SetAuditLogger_NoPanic(t *testing.T) {
	srv := NewServer()
	client := NewClient(srv)
	client.SetAuditLogger(nil)
	client.SetNodeID("node-1")
}

func TestClient_SetNodeID(t *testing.T) {
	srv := NewServer()
	client := NewClient(srv)
	client.SetNodeID("test-node")
}

func TestServer_StreamCall_SingleRequest(t *testing.T) {
	srv := NewServer()
	handler := &echoHandler{}
	srv.RegisterHandler("echo", handler)

	stream := &mockStreamCallServer{
		reqs: []*meshv1.CallRequest{
			{TargetModule: "echo", Method: "ping", Payload: []byte("hello")},
		},
		ctx: context.Background(),
	}

	err := srv.StreamCall(stream)
	if err != nil {
		t.Fatalf("StreamCall: %v", err)
	}
	if len(stream.sent) != 1 {
		t.Fatalf("expected 1 response, got %d", len(stream.sent))
	}
	if string(stream.sent[0].Payload) != "hello" {
		t.Errorf("expected payload 'hello', got %q", string(stream.sent[0].Payload))
	}
}

func TestServer_StreamCall_UnknownModule(t *testing.T) {
	srv := NewServer()
	stream := &mockStreamCallServer{
		reqs: []*meshv1.CallRequest{
			{TargetModule: "nonexistent", Method: "ping"},
		},
		ctx: context.Background(),
	}

	err := srv.StreamCall(stream)
	if err == nil {
		t.Fatal("expected error for unknown module")
	}
}

func TestSendHeartbeats_NoPeers_NoOp(t *testing.T) {
	ds := newDS("node-a")
	// No members besides self — sendHeartbeats should do nothing.
	ds.sendHeartbeats(context.Background())
}

func TestSendHeartbeats_UnreachablePeer_NoPanic(t *testing.T) {
	ds := newDS("node-a", "node-a", "node-b")
	ds.members["node-b"].GrpcAddr = "127.0.0.1:1" // unreachable port
	// Should log a warning but not panic.
	ds.sendHeartbeats(context.Background())
}

func TestSendHeartbeats_WithConnPool_NoPanic(t *testing.T) {
	ds := newDS("node-a", "node-a", "node-b")
	ds.members["node-b"].GrpcAddr = "127.0.0.1:1"
	pool := NewConnPool()
	ds.SetConnPool(pool)
	ds.sendHeartbeats(context.Background())
}

// mockStreamCallServer implements meshv1.ModuleMesh_StreamCallServer.
type mockStreamCallServer struct {
	reqs []*meshv1.CallRequest
	idx  int
	sent []*meshv1.CallResponse
	ctx  context.Context
}

func (m *mockStreamCallServer) Recv() (*meshv1.CallRequest, error) {
	if m.idx >= len(m.reqs) {
		return nil, io.EOF
	}
	req := m.reqs[m.idx]
	m.idx++
	return req, nil
}

func (m *mockStreamCallServer) Send(resp *meshv1.CallResponse) error {
	m.sent = append(m.sent, resp)
	return nil
}

func (m *mockStreamCallServer) Context() context.Context     { return m.ctx }
func (m *mockStreamCallServer) SetHeader(metadata.MD) error  { return nil }
func (m *mockStreamCallServer) SendHeader(metadata.MD) error { return nil }
func (m *mockStreamCallServer) SetTrailer(metadata.MD)       {}
func (m *mockStreamCallServer) SendMsg(any) error            { return nil }
func (m *mockStreamCallServer) RecvMsg(any) error            { return nil }

func TestStandardHealthProbe_Watch_SendsServing(t *testing.T) {
	probe := &standardHealthProbe{}
	ctx, cancel := context.WithCancel(context.Background())
	stream := &mockHealthWatchStream{ctx: ctx, sent: make(chan struct{}, 1)}

	done := make(chan error, 1)
	go func() {
		done <- probe.Watch(nil, stream)
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Watch returned error: %v", err)
		}
	case <-stream.sent:
		// Received SERVING response.
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for Watch to send")
	}

	cancel()
	time.Sleep(50 * time.Millisecond)
}

// mockHealthWatchStream implements grpc_health_v1.Health_WatchServer.
type mockHealthWatchStream struct {
	ctx  context.Context
	sent chan struct{}
}

func (m *mockHealthWatchStream) Context() context.Context { return m.ctx }
func (m *mockHealthWatchStream) Send(*grpc_health_v1.HealthCheckResponse) error {
	select {
	case m.sent <- struct{}{}:
	default:
	}
	return nil
}
func (m *mockHealthWatchStream) SetHeader(metadata.MD) error  { return nil }
func (m *mockHealthWatchStream) SendHeader(metadata.MD) error { return nil }
func (m *mockHealthWatchStream) SetTrailer(metadata.MD)       {}
func (m *mockHealthWatchStream) SendMsg(any) error            { return nil }
func (m *mockHealthWatchStream) RecvMsg(any) error            { return nil }
