//go:build integration

// SDK integration tests validate that the Go SDK client's wire protocol
// matches what the in-process gRPC server expects. Since sdk/go/client is
// a separate go module, we reproduce its core patterns here using the raw
// gRPC generated stubs — the SDK is tested by sdk_client_test.go separately.

package integration

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"testing"
	"time"

	healthv1 "github.com/Muxcore-Media/core/proto/gen/muxcore/health/v1"
	storagev1 "github.com/Muxcore-Media/core/proto/gen/muxcore/storage/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// dialTest opens a raw gRPC connection to the harness for SDK-style tests.
func dialTest(t *testing.T, addr string) *grpc.ClientConn {
	t.Helper()
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	return conn
}

func TestSDKWire_StoragePutGetDelete(t *testing.T) {
	h := newHarness(t)

	prov := newMemStorage()
	mod := &storageModule{id: "storage-wire-test", prov: prov}
	h.modMgr.Register(mod, nil)
	h.store.DiscoverStorage()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	conn := dialTest(t, h.grpcAddr)
	client := storagev1.NewStorageServiceClient(conn)

	content := []byte("sdk wire integration test")

	// Put
	stream, err := client.Put(ctx)
	if err != nil {
		t.Fatalf("Put stream: %v", err)
	}
	stream.Send(&storagev1.PutRequest{
		Key:       "wire/file.txt",
		Chunk:     content,
		TotalSize: int64(len(content)),
	})
	if _, err := stream.CloseAndRecv(); err != nil {
		t.Fatalf("Put CloseAndRecv: %v", err)
	}

	// Get
	getStream, err := client.Get(ctx, &storagev1.GetRequest{Key: "wire/file.txt"})
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	var got []byte
	for {
		chunk, err := getStream.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("Get Recv: %v", err)
		}
		got = append(got, chunk.GetChunk()...)
	}
	if !bytes.Equal(got, content) {
		t.Errorf("Get returned %q, want %q", got, content)
	}

	// Delete
	resp, err := client.Delete(ctx, &storagev1.DeleteRequest{Key: "wire/file.txt"})
	if err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if !resp.Deleted {
		t.Error("expected Deleted=true")
	}
}

func TestSDKWire_HealthCheck(t *testing.T) {
	h := newHarness(t)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn := dialTest(t, h.grpcAddr)
	client := healthv1.NewHealthServiceClient(conn)

	resp, err := client.Check(ctx, &healthv1.HealthCheckRequest{})
	if err != nil {
		t.Fatalf("Health.Check: %v", err)
	}
	if resp == nil {
		t.Fatal("expected non-nil health response")
	}
}

func TestSDKWire_StorageList(t *testing.T) {
	h := newHarness(t)

	prov := newMemStorage()
	prov.objects["sdklist/a.txt"] = []byte("a")
	prov.objects["sdklist/b.txt"] = []byte("bb")
	mod := &storageModule{id: "storage-list-wire", prov: prov}
	h.modMgr.Register(mod, nil)
	h.store.DiscoverStorage()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn := dialTest(t, h.grpcAddr)
	client := storagev1.NewStorageServiceClient(conn)

	resp, err := client.List(ctx, &storagev1.ListRequest{Prefix: "sdklist/"})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(resp.GetObjects()) != 2 {
		t.Errorf("expected 2 objects, got %d", len(resp.GetObjects()))
	}
}

// Ensure integration test file references net package.
var _ = net.Listen
