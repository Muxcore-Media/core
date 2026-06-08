package grpcmesh

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"testing"

	"github.com/Muxcore-Media/core/pkg/contracts"
	storagev1 "github.com/Muxcore-Media/core/proto/gen/muxcore/storage/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
)

// allowAllPolicy is a CallPolicyProvider that permits everything.
type allowAllStoragePolicy struct{}

func (allowAllStoragePolicy) AllowCall(_ context.Context, _, _, _ string) (bool, error) {
	return true, nil
}

// denyAllPolicy denies every call.
type denyAllStoragePolicy struct{}

func (denyAllStoragePolicy) AllowCall(_ context.Context, _, _, _ string) (bool, error) {
	return false, nil
}

// stubOrchestrator is an in-memory StorageOrchestrator for testing.
type stubOrchestrator struct {
	objects map[string][]byte
}

func newStubOrch() *stubOrchestrator {
	return &stubOrchestrator{objects: make(map[string][]byte)}
}

func (o *stubOrchestrator) Put(_ context.Context, key string, data io.Reader, _ int64) error {
	b, err := io.ReadAll(data)
	if err != nil {
		return err
	}
	o.objects[key] = b
	return nil
}
func (o *stubOrchestrator) Get(_ context.Context, key string) (io.ReadCloser, error) {
	b, ok := o.objects[key]
	if !ok {
		return nil, contracts.ErrNotFound
	}
	return io.NopCloser(bytes.NewReader(b)), nil
}
func (o *stubOrchestrator) Delete(_ context.Context, key string) error {
	if _, ok := o.objects[key]; !ok {
		return contracts.ErrNotFound
	}
	delete(o.objects, key)
	return nil
}
func (o *stubOrchestrator) Move(_ context.Context, src, dst string) error {
	b, ok := o.objects[src]
	if !ok {
		return contracts.ErrNotFound
	}
	delete(o.objects, src)
	o.objects[dst] = b
	return nil
}
func (o *stubOrchestrator) Exists(_ context.Context, key string) (bool, error) {
	_, ok := o.objects[key]
	return ok, nil
}
func (o *stubOrchestrator) Stat(_ context.Context, key string) (contracts.ObjectInfo, error) {
	b, ok := o.objects[key]
	if !ok {
		return contracts.ObjectInfo{}, contracts.ErrNotFound
	}
	return contracts.ObjectInfo{Key: key, Size: int64(len(b))}, nil
}
func (o *stubOrchestrator) List(_ context.Context, _ string) ([]contracts.ObjectInfo, error) {
	out := make([]contracts.ObjectInfo, 0, len(o.objects))
	for k, b := range o.objects {
		out = append(out, contracts.ObjectInfo{Key: k, Size: int64(len(b))})
	}
	return out, nil
}
func (o *stubOrchestrator) Stream(_ context.Context, key string, offset, length int64) (io.ReadCloser, error) {
	return o.Get(context.Background(), key)
}
func (o *stubOrchestrator) ProviderCount() int { return 1 }
func (o *stubOrchestrator) CapabilityCheck(_ context.Context, _ string) ([]string, error) {
	return []string{"streamable"}, nil
}

// startStorageServer starts a real gRPC storage server on a random port and
// returns the client and a cleanup function.
func startStorageServer(t *testing.T, orch contracts.StorageOrchestrator, policy contracts.CallPolicyProvider) storagev1.StorageServiceClient {
	t.Helper()
	srv := NewStorageServer(orch)
	srv.SetCallPolicy(policy)

	lis, err := net.Listen("tcp", "127.0.0.1:0")
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

	return storagev1.NewStorageServiceClient(conn)
}

// --- Put ---

func TestStorageServer_Put_Simple(t *testing.T) {
	orch := newStubOrch()
	client := startStorageServer(t, orch, allowAllStoragePolicy{})

	stream, err := client.Put(context.Background())
	if err != nil {
		t.Fatalf("Put stream: %v", err)
	}
	if err := stream.Send(&storagev1.PutRequest{Key: "test/file.txt", TotalSize: 11, Chunk: []byte("hello world")}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	resp, err := stream.CloseAndRecv()
	if err != nil {
		t.Fatalf("CloseAndRecv: %v", err)
	}
	if resp.Key != "test/file.txt" {
		t.Errorf("expected key 'test/file.txt', got %q", resp.Key)
	}
	if _, ok := orch.objects["test/file.txt"]; !ok {
		t.Error("expected object to be stored")
	}
}

func TestStorageServer_Put_KeyRequired(t *testing.T) {
	orch := newStubOrch()
	client := startStorageServer(t, orch, allowAllStoragePolicy{})

	stream, _ := client.Put(context.Background())
	stream.Send(&storagev1.PutRequest{Chunk: []byte("data")}) // no key
	_, err := stream.CloseAndRecv()
	if err == nil {
		t.Fatal("expected error for missing key")
	}
	st, _ := status.FromError(err)
	if st.Code() != codes.InvalidArgument {
		t.Errorf("expected InvalidArgument, got %s", st.Code())
	}
}

func TestStorageServer_Put_DeniedByPolicy(t *testing.T) {
	orch := newStubOrch()
	client := startStorageServer(t, orch, denyAllStoragePolicy{})

	stream, err := client.Put(context.Background())
	if err != nil {
		t.Fatalf("Put stream open: %v", err)
	}
	stream.Send(&storagev1.PutRequest{Key: "x", Chunk: []byte("y")})
	_, err = stream.CloseAndRecv()
	if err == nil {
		t.Fatal("expected error when denied by policy")
	}
	st, _ := status.FromError(err)
	if st.Code() != codes.PermissionDenied {
		t.Errorf("expected PermissionDenied, got %s", st.Code())
	}
}

// --- Get ---

func TestStorageServer_Get_Simple(t *testing.T) {
	orch := newStubOrch()
	orch.objects["media/file.txt"] = []byte("content here")
	client := startStorageServer(t, orch, allowAllStoragePolicy{})

	stream, err := client.Get(context.Background(), &storagev1.GetRequest{Key: "media/file.txt"})
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	var data []byte
	for {
		chunk, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("Recv: %v", err)
		}
		data = append(data, chunk.GetChunk()...)
	}
	if string(data) != "content here" {
		t.Errorf("expected 'content here', got %q", data)
	}
}

func TestStorageServer_Get_NotFound(t *testing.T) {
	orch := newStubOrch()
	client := startStorageServer(t, orch, allowAllStoragePolicy{})

	stream, err := client.Get(context.Background(), &storagev1.GetRequest{Key: "no/such/key"})
	if err != nil {
		t.Fatalf("Get open: %v", err)
	}
	_, err = stream.Recv()
	if err == nil {
		t.Fatal("expected error for missing key")
	}
	st, _ := status.FromError(err)
	if st.Code() != codes.NotFound {
		t.Errorf("expected NotFound, got %s", st.Code())
	}
}

func TestStorageServer_Get_KeyRequired(t *testing.T) {
	orch := newStubOrch()
	client := startStorageServer(t, orch, allowAllStoragePolicy{})

	stream, err := client.Get(context.Background(), &storagev1.GetRequest{})
	if err != nil {
		t.Fatalf("Get open: %v", err)
	}
	_, err = stream.Recv()
	if err == nil {
		t.Fatal("expected error for empty key")
	}
	st, _ := status.FromError(err)
	if st.Code() != codes.InvalidArgument {
		t.Errorf("expected InvalidArgument, got %s", st.Code())
	}
}

// --- Delete ---

func TestStorageServer_Delete_Success(t *testing.T) {
	orch := newStubOrch()
	orch.objects["to-delete"] = []byte("bye")
	client := startStorageServer(t, orch, allowAllStoragePolicy{})

	resp, err := client.Delete(context.Background(), &storagev1.DeleteRequest{Key: "to-delete"})
	if err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if !resp.Deleted {
		t.Error("expected Deleted=true")
	}
	if _, ok := orch.objects["to-delete"]; ok {
		t.Error("expected object to be removed")
	}
}

func TestStorageServer_Delete_NotFound(t *testing.T) {
	orch := newStubOrch()
	client := startStorageServer(t, orch, allowAllStoragePolicy{})

	resp, err := client.Delete(context.Background(), &storagev1.DeleteRequest{Key: "no/such/key"})
	if err != nil {
		t.Fatalf("Delete not-found should not return gRPC error: %v", err)
	}
	if resp.Deleted {
		t.Error("expected Deleted=false for missing key")
	}
}

func TestStorageServer_Delete_ProviderError(t *testing.T) {
	// An orchestrator that returns a non-ErrNotFound error.
	orch := &errorOrch{err: errors.New("disk full")}
	client := startStorageServer(t, orch, allowAllStoragePolicy{})

	_, err := client.Delete(context.Background(), &storagev1.DeleteRequest{Key: "any"})
	if err == nil {
		t.Fatal("expected error for provider failure")
	}
	st, _ := status.FromError(err)
	if st.Code() != codes.Internal {
		t.Errorf("expected Internal for provider error, got %s", st.Code())
	}
}

func TestStorageServer_Delete_KeyRequired(t *testing.T) {
	orch := newStubOrch()
	client := startStorageServer(t, orch, allowAllStoragePolicy{})

	_, err := client.Delete(context.Background(), &storagev1.DeleteRequest{})
	if err == nil {
		t.Fatal("expected error for empty key")
	}
	st, _ := status.FromError(err)
	if st.Code() != codes.InvalidArgument {
		t.Errorf("expected InvalidArgument, got %s", st.Code())
	}
}

// --- Stat ---

func TestStorageServer_Stat_Found(t *testing.T) {
	orch := newStubOrch()
	orch.objects["file.bin"] = []byte("1234567890")
	client := startStorageServer(t, orch, allowAllStoragePolicy{})

	resp, err := client.Stat(context.Background(), &storagev1.StatRequest{Key: "file.bin"})
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if resp.Size != 10 {
		t.Errorf("expected size 10, got %d", resp.Size)
	}
}

// --- List ---

func TestStorageServer_List(t *testing.T) {
	orch := newStubOrch()
	orch.objects["media/a.mkv"] = []byte("a")
	orch.objects["media/b.mkv"] = []byte("bb")
	client := startStorageServer(t, orch, allowAllStoragePolicy{})

	resp, err := client.List(context.Background(), &storagev1.ListRequest{Prefix: "media/"})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(resp.GetObjects()) != 2 {
		t.Errorf("expected 2 objects, got %d", len(resp.GetObjects()))
	}
}

// --- Capabilities ---

func TestStorageServer_Capabilities(t *testing.T) {
	orch := newStubOrch()
	client := startStorageServer(t, orch, allowAllStoragePolicy{})

	resp, err := client.Capabilities(context.Background(), &storagev1.CapabilitiesRequest{})
	if err != nil {
		t.Fatalf("Capabilities: %v", err)
	}
	if len(resp.GetCapabilities()) == 0 {
		t.Error("expected at least one capability")
	}
}

// --- No policy ---

func TestStorageServer_NoPolicy_Denied(t *testing.T) {
	orch := newStubOrch()
	srv := NewStorageServer(orch) // no policy set

	lis, _ := net.Listen("tcp", "127.0.0.1:0")
	grpcSrv := grpc.NewServer()
	srv.RegisterWithGRPC(grpcSrv)
	go grpcSrv.Serve(lis)
	defer grpcSrv.GracefulStop()

	conn, _ := grpc.NewClient(lis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	defer conn.Close()

	client := storagev1.NewStorageServiceClient(conn)
	_, err := client.List(context.Background(), &storagev1.ListRequest{Prefix: "x"})
	if err == nil {
		t.Fatal("expected error when no call policy configured")
	}
	st, _ := status.FromError(err)
	if st.Code() != codes.PermissionDenied {
		t.Errorf("expected PermissionDenied, got %s", st.Code())
	}
}

// errorOrch always returns the given error from all operations.
type errorOrch struct{ err error }

func (o *errorOrch) Put(_ context.Context, _ string, _ io.Reader, _ int64) error { return o.err }
func (o *errorOrch) Get(_ context.Context, _ string) (io.ReadCloser, error)      { return nil, o.err }
func (o *errorOrch) Delete(_ context.Context, _ string) error                    { return o.err }
func (o *errorOrch) Move(_ context.Context, _, _ string) error                   { return o.err }
func (o *errorOrch) Exists(_ context.Context, _ string) (bool, error)            { return false, o.err }
func (o *errorOrch) Stat(_ context.Context, _ string) (contracts.ObjectInfo, error) {
	return contracts.ObjectInfo{}, o.err
}
func (o *errorOrch) List(_ context.Context, _ string) ([]contracts.ObjectInfo, error) {
	return nil, o.err
}
func (o *errorOrch) Stream(_ context.Context, _ string, _, _ int64) (io.ReadCloser, error) {
	return nil, o.err
}
func (o *errorOrch) ProviderCount() int { return 0 }
func (o *errorOrch) CapabilityCheck(_ context.Context, _ string) ([]string, error) {
	return nil, o.err
}

var _ = fmt.Sprintf // silence unused import warning
