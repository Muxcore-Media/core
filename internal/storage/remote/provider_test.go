package remote_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"strings"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

	"github.com/Muxcore-Media/core/internal/storage/remote"
	"github.com/Muxcore-Media/core/pkg/contracts"
	storagev1 "github.com/Muxcore-Media/core/proto/gen/muxcore/storage/v1"
)

type memServer struct {
	storagev1.UnimplementedStorageServiceServer
	objects map[string][]byte
}

func newMemServer() *memServer {
	return &memServer{objects: map[string][]byte{}}
}

func (s *memServer) Put(stream storagev1.StorageService_PutServer) error {
	var key string
	var buf bytes.Buffer
	for {
		msg, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		if msg.GetKey() != "" {
			key = msg.GetKey()
		}
		if c := msg.GetChunk(); len(c) > 0 {
			_, _ = buf.Write(c)
		}
	}
	s.objects[key] = buf.Bytes()
	return stream.SendAndClose(&storagev1.PutResponse{Key: key, Size: int64(buf.Len())})
}

func (s *memServer) Get(req *storagev1.GetRequest, stream storagev1.StorageService_GetServer) error {
	body, ok := s.objects[req.GetKey()]
	if !ok {
		return status.Error(codes.NotFound, "missing")
	}
	start := int(req.GetOffset())
	if start < 0 {
		start = 0
	}
	end := len(body)
	if req.GetLength() > 0 && start+int(req.GetLength()) < end {
		end = start + int(req.GetLength())
	}
	if start > len(body) {
		start = len(body)
	}
	chunk := body[start:end]
	return stream.Send(&storagev1.GetResponse{Chunk: chunk, TotalSize: int64(len(body))})
}

func (s *memServer) Delete(_ context.Context, req *storagev1.DeleteRequest) (*storagev1.DeleteResponse, error) {
	_, ok := s.objects[req.GetKey()]
	delete(s.objects, req.GetKey())
	return &storagev1.DeleteResponse{Deleted: ok}, nil
}

func (s *memServer) Stat(_ context.Context, req *storagev1.StatRequest) (*storagev1.StatResponse, error) {
	body, ok := s.objects[req.GetKey()]
	if !ok {
		return &storagev1.StatResponse{Found: false, Key: req.GetKey()}, nil
	}
	return &storagev1.StatResponse{Found: true, Key: req.GetKey(), Size: int64(len(body))}, nil
}

func (s *memServer) List(_ context.Context, req *storagev1.ListRequest) (*storagev1.ListResponse, error) {
	var out []*storagev1.StatResponse
	for k, v := range s.objects {
		if req.GetPrefix() == "" || strings.HasPrefix(k, req.GetPrefix()) {
			out = append(out, &storagev1.StatResponse{Found: true, Key: k, Size: int64(len(v))})
		}
	}
	return &storagev1.ListResponse{Objects: out}, nil
}

func (s *memServer) Capabilities(context.Context, *storagev1.CapabilitiesRequest) (*storagev1.CapabilitiesResponse, error) {
	return &storagev1.CapabilitiesResponse{Capabilities: []string{"streamable"}}, nil
}

func startServer(t *testing.T) *remote.Provider {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	gs := grpc.NewServer()
	storagev1.RegisterStorageServiceServer(gs, newMemServer())
	go func() { _ = gs.Serve(lis) }()
	t.Cleanup(func() { gs.Stop(); _ = lis.Close() })

	conn, err := grpc.NewClient(lis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return remote.New("test-storage", conn)
}

func TestRemoteProviderRoundTrip(t *testing.T) {
	p := startServer(t)
	ctx := context.Background()
	body := []byte("hello-remote")
	if err := p.Put(ctx, "k1", bytes.NewReader(body), int64(len(body))); err != nil {
		t.Fatalf("Put: %v", err)
	}
	rc, err := p.Get(ctx, "k1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	got, err := io.ReadAll(rc)
	_ = rc.Close()
	if err != nil || !bytes.Equal(got, body) {
		t.Fatalf("got=%q err=%v", got, err)
	}
	info, err := p.Stat(ctx, "k1")
	if err != nil || info.Size != int64(len(body)) {
		t.Fatalf("Stat: %+v err=%v", info, err)
	}
	ok, err := p.Exists(ctx, "k1")
	if err != nil || !ok {
		t.Fatalf("Exists: %v %v", ok, err)
	}
	list, err := p.List(ctx, "k")
	if err != nil || len(list) != 1 {
		t.Fatalf("List: %+v err=%v", list, err)
	}
	if err := p.Move(ctx, "k1", "k2"); err != nil {
		t.Fatalf("Move: %v", err)
	}
	ok, _ = p.Exists(ctx, "k1")
	if ok {
		t.Fatal("src still exists")
	}
	if err := p.Delete(ctx, "k2"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	_, err = p.Get(ctx, "missing")
	if !errors.Is(err, contracts.ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}
