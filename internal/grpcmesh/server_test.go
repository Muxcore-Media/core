package grpcmesh

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/Muxcore-Media/core/pkg/contracts"
	meshv1 "github.com/Muxcore-Media/core/proto/gen/muxcore/mesh/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
)

// --- Stubs ---

type stubMeshHandler struct {
	payload []byte
	err     error
}

func (h *stubMeshHandler) HandleCall(_ context.Context, method string, payload []byte) ([]byte, error) {
	return h.payload, h.err
}

type stubCallPolicyErr struct {
	allow bool
	err   error
}

func (p *stubCallPolicyErr) AllowCall(_ context.Context, _, _, _ string) (bool, error) {
	return p.allow, p.err
}

// --- Tests ---

func TestNewServer(t *testing.T) {
	srv := NewServer()
	if srv == nil {
		t.Fatal("expected non-nil server")
	}
	if srv.handlers == nil {
		t.Error("expected handlers map to be initialized")
	}
}

func TestRegisterHandler(t *testing.T) {
	srv := NewServer()
	handler := &stubMeshHandler{payload: []byte("ok")}
	srv.RegisterHandler("test-module", handler)

	srv.mu.RLock()
	h, ok := srv.handlers["test-module"]
	srv.mu.RUnlock()
	if !ok {
		t.Fatal("expected handler to be registered")
	}
	if h != handler {
		t.Error("expected same handler reference")
	}
}

func TestCall_RoutesToRegisteredHandler(t *testing.T) {
	srv := NewServer()
	srv.RegisterHandler("math", &stubMeshHandler{payload: []byte("42")})

	client := NewClient(srv)
	client.SetCallPolicy(stubCallPolicy{allow: true})

	result, err := client.Call(context.Background(), "math", "add", []byte(`[1, 2]`))
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if string(result) != "42" {
		t.Errorf("expected '42', got %q", string(result))
	}
}

func TestCall_UnknownModule(t *testing.T) {
	srv := NewServer()
	client := NewClient(srv)
	client.SetCallPolicy(stubCallPolicy{allow: true})

	_, err := client.Call(context.Background(), "no-such-module", "ping", nil)
	if err == nil {
		t.Fatal("expected error for unknown module")
	}
}

func TestCall_NoPolicy_Denied(t *testing.T) {
	srv := NewServer()
	srv.RegisterHandler("math", &stubMeshHandler{payload: []byte("42")})

	client := NewClient(srv)

	_, err := client.Call(context.Background(), "math", "add", nil)
	if err == nil {
		t.Fatal("expected denial when no call policy configured")
	}
}

func TestCall_PolicyDenies(t *testing.T) {
	srv := NewServer()
	srv.RegisterHandler("math", &stubMeshHandler{payload: []byte("42")})

	client := NewClient(srv)
	client.SetCallPolicy(stubCallPolicy{allow: false})

	_, err := client.Call(context.Background(), "math", "add", nil)
	if err == nil {
		t.Fatal("expected error when policy denies call")
	}
}

func TestCall_PolicyError(t *testing.T) {
	srv := NewServer()
	srv.RegisterHandler("math", &stubMeshHandler{payload: []byte("42")})

	client := NewClient(srv)
	client.SetCallPolicy(&stubCallPolicyErr{allow: false, err: errors.New("policy engine down")})

	_, err := client.Call(context.Background(), "math", "add", nil)
	if err == nil {
		t.Fatal("expected error when policy engine fails")
	}
}

func TestCall_HandlerError(t *testing.T) {
	srv := NewServer()
	srv.RegisterHandler("faulty", &stubMeshHandler{err: errors.New("handler: internal error")})

	client := NewClient(srv)
	client.SetCallPolicy(stubCallPolicy{allow: true})

	_, err := client.Call(context.Background(), "faulty", "crash", nil)
	if err == nil {
		t.Fatal("expected error when handler returns error")
	}
}

func TestRegisterWithGRPC(t *testing.T) {
	srv := NewServer()
	srv.RegisterHandler("echo", &stubMeshHandler{payload: []byte("pong")})

	var lc net.ListenConfig
	lis, err := lc.Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	grpcSrv := grpc.NewServer()
	srv.RegisterWithGRPC(grpcSrv)
	go grpcSrv.Serve(lis)
	t.Cleanup(grpcSrv.GracefulStop)

	conn, err := grpc.NewClient(lis.Addr().String(),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { conn.Close() })

	client := meshv1.NewModuleMeshClient(conn)
	resp, err := client.Call(context.Background(), &meshv1.CallRequest{
		TargetModule: "echo",
		Method:       "ping",
	})
	if err != nil {
		t.Fatalf("Call via gRPC: %v", err)
	}
	if string(resp.Payload) != "pong" {
		t.Errorf("expected 'pong', got %q", string(resp.Payload))
	}
}

func TestRegisterWithGRPC_UnknownModule(t *testing.T) {
	srv := NewServer()

	var lc net.ListenConfig
	lis, err := lc.Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	grpcSrv := grpc.NewServer()
	srv.RegisterWithGRPC(grpcSrv)
	go grpcSrv.Serve(lis)
	t.Cleanup(grpcSrv.GracefulStop)

	conn, err := grpc.NewClient(lis.Addr().String(),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { conn.Close() })

	client := meshv1.NewModuleMeshClient(conn)
	_, err = client.Call(context.Background(), &meshv1.CallRequest{
		TargetModule: "no-such",
		Method:       "ping",
	})
	if err == nil {
		t.Fatal("expected error for unknown module via gRPC")
	}
	st, _ := status.FromError(err)
	if st.Code() != codes.NotFound {
		t.Errorf("expected NotFound, got %s", st.Code())
	}
}

func TestConcurrentRegistrationAndCalls(t *testing.T) {
	srv := NewServer()
	client := NewClient(srv)
	client.SetCallPolicy(stubCallPolicy{allow: true})

	var wg sync.WaitGroup

	// Concurrently register handlers.
	for i := range 10 {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			srv.RegisterHandler(
				fmt.Sprintf("mod-%d", id),
				&stubMeshHandler{payload: []byte(fmt.Sprintf("%d", id))},
			)
		}(i)
	}
	wg.Wait()

	// Concurrently call them.
	results := make([]string, 10)
	var mu sync.Mutex
	for i := range 10 {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			result, err := client.Call(context.Background(), fmt.Sprintf("mod-%d", id), "get", nil)
			if err != nil {
				t.Errorf("Call mod-%d: %v", id, err)
				return
			}
			mu.Lock()
			results[id] = string(result)
			mu.Unlock()
		}(i)
	}
	wg.Wait()

	for i := range 10 {
		expected := fmt.Sprintf("%d", i)
		if results[i] != expected {
			t.Errorf("mod-%d: expected %q, got %q", i, expected, results[i])
		}
	}
}

func TestAuditLoggerSet(t *testing.T) {
	srv := NewServer()
	srv.SetNodeID("node-1")

	logged := make(chan struct{})
	srv.SetAuditLogger(&stubAuditLogger{
		logFn: func(entry contracts.AuditEntry) {
			if entry.Resource != "audit-mod" {
				t.Errorf("expected resource 'audit-mod', got %q", entry.Resource)
			}
			close(logged)
		},
	})

	srv.RegisterHandler("audit-mod", &stubMeshHandler{payload: []byte("ok")})

	// Audit logging happens in Server.Call (the gRPC handler), so
	// we need to test through the gRPC path.
	var lc net.ListenConfig
	lis, err := lc.Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	grpcSrv := grpc.NewServer()
	srv.RegisterWithGRPC(grpcSrv)
	go grpcSrv.Serve(lis)
	t.Cleanup(grpcSrv.GracefulStop)

	conn, err := grpc.NewClient(lis.Addr().String(),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { conn.Close() })

	client := meshv1.NewModuleMeshClient(conn)
	_, err = client.Call(context.Background(), &meshv1.CallRequest{
		TargetModule: "audit-mod",
		Method:       "ping",
	})
	if err != nil {
		t.Fatalf("Call via gRPC: %v", err)
	}

	select {
	case <-logged:
	case <-time.After(time.Second):
		t.Error("expected audit logger to be called")
	}
}

func TestRegisterHandler_Overwrite(t *testing.T) {
	srv := NewServer()
	h1 := &stubMeshHandler{payload: []byte("first")}
	h2 := &stubMeshHandler{payload: []byte("second")}

	srv.RegisterHandler("mod", h1)
	srv.RegisterHandler("mod", h2)

	client := NewClient(srv)
	client.SetCallPolicy(stubCallPolicy{allow: true})

	result, err := client.Call(context.Background(), "mod", "get", nil)
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if string(result) != "second" {
		t.Errorf("expected 'second', got %q", string(result))
	}
}

func TestClient_CallPolicyRetrieval(t *testing.T) {
	srv := NewServer()
	client := NewClient(srv)

	if client.CallPolicy() != nil {
		t.Error("expected nil call policy initially")
	}

	policy := stubCallPolicy{allow: true}
	client.SetCallPolicy(policy)
	if client.CallPolicy() != policy {
		t.Error("expected same policy reference")
	}
}

// stubAuditLogger for testing audit logging.
type stubAuditLogger struct {
	logFn func(contracts.AuditEntry)
}

func (l *stubAuditLogger) Log(_ context.Context, entry contracts.AuditEntry) error {
	l.logFn(entry)
	return nil
}

func (l *stubAuditLogger) Query(_ context.Context, _ contracts.AuditFilter) ([]contracts.AuditEntry, error) {
	return nil, nil
}

func (l *stubAuditLogger) Export(_ context.Context, _ string) (io.ReadCloser, error) {
	return nil, nil
}

func (l *stubAuditLogger) VerifyChainIntegrity(_ context.Context, _, _ time.Time) (contracts.ChainVerificationResult, error) {
	return contracts.ChainVerificationResult{Valid: true}, nil
}
func (l *stubAuditLogger) VerifyAll(_ context.Context) (contracts.ChainVerificationResult, error) {
	return contracts.ChainVerificationResult{Valid: true}, nil
}
