package client

import (
	"bytes"
	"context"
	"io"
	"net"
	"testing"

	storagev1 "github.com/Muxcore-Media/core/proto/gen/muxcore/storage/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
)

type capturePutServer struct {
	storagev1.UnimplementedStorageServiceServer
	frames []*storagev1.PutRequest
}

func (s *capturePutServer) Put(stream storagev1.StorageService_PutServer) error {
	var wrote int64
	for {
		req, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		s.frames = append(s.frames, &storagev1.PutRequest{
			Key:       req.GetKey(),
			TotalSize: req.GetTotalSize(),
			Chunk:     append([]byte(nil), req.GetChunk()...),
		})
		wrote += int64(len(req.GetChunk()))
	}
	return stream.SendAndClose(&storagev1.PutResponse{Key: s.frames[0].Key, Size: wrote})
}

func TestStoragePut_ChunksLargePayload(t *testing.T) {
	lis := bufconn.Listen(1 << 20)
	gs := grpc.NewServer()
	srv := &capturePutServer{}
	storagev1.RegisterStorageServiceServer(gs, srv)
	go gs.Serve(lis)
	t.Cleanup(func() { gs.Stop(); lis.Close() })

	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return lis.Dial() }),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })

	sc := &StorageClient{raw: storagev1.NewStorageServiceClient(conn)}
	// 2.5 MiB → at least 3 chunk frames with 1 MiB putChunkSize
	payload := bytes.Repeat([]byte("abcdefgh"), (putChunkSize*2+putChunkSize/2)/8)
	if err := sc.Put(context.Background(), "media/big.bin", bytes.NewReader(payload)); err != nil {
		t.Fatal(err)
	}
	if len(srv.frames) < 3 {
		t.Fatalf("expected chunked frames, got %d", len(srv.frames))
	}
	if srv.frames[0].Key != "media/big.bin" {
		t.Fatalf("first frame key=%q", srv.frames[0].Key)
	}
	if srv.frames[0].TotalSize != int64(len(payload)) {
		t.Fatalf("total_size=%d want %d", srv.frames[0].TotalSize, len(payload))
	}
	for i, f := range srv.frames[1:] {
		if f.Key != "" {
			t.Fatalf("frame %d should not repeat key", i+1)
		}
	}
	var got []byte
	for _, f := range srv.frames {
		got = append(got, f.Chunk...)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("reassembled len=%d want %d", len(got), len(payload))
	}
}

func TestStoragePut_Empty(t *testing.T) {
	lis := bufconn.Listen(1 << 20)
	gs := grpc.NewServer()
	srv := &capturePutServer{}
	storagev1.RegisterStorageServiceServer(gs, srv)
	go gs.Serve(lis)
	t.Cleanup(func() { gs.Stop(); lis.Close() })

	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return lis.Dial() }),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })

	sc := &StorageClient{raw: storagev1.NewStorageServiceClient(conn)}
	if err := sc.Put(context.Background(), "empty", bytes.NewReader(nil)); err != nil {
		t.Fatal(err)
	}
	if len(srv.frames) != 1 || srv.frames[0].Key != "empty" {
		t.Fatalf("frames=%d", len(srv.frames))
	}
}
