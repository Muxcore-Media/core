// Package remote provides a gRPC StorageProvider that dials sidecar modules
// exposing muxcore.storage.v1.StorageService (e.g. storage-s3).
package remote

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/Muxcore-Media/core/pkg/contracts"
	storagev1 "github.com/Muxcore-Media/core/proto/gen/muxcore/storage/v1"
)

const putChunkSize = 1 << 20 // 1 MiB

// Provider implements contracts.StorageProvider (+ Streamable) over gRPC.
//
//nolint:govet // fieldalignment: id kept first for readability
type Provider struct {
	id     string
	client storagev1.StorageServiceClient
	conn   *grpc.ClientConn
}

// New wraps an existing client connection to a storage sidecar.
func New(moduleID string, conn *grpc.ClientConn) *Provider {
	return &Provider{
		id:     moduleID,
		client: storagev1.NewStorageServiceClient(conn),
		conn:   conn,
	}
}

// ID returns the discovered module id.
func (p *Provider) ID() string { return p.id }

// Close closes the underlying connection when owned by this provider.
func (p *Provider) Close() error {
	if p.conn != nil {
		return p.conn.Close()
	}
	return nil
}

func (p *Provider) Put(ctx context.Context, key string, data io.Reader, size int64) error {
	stream, err := p.client.Put(ctx)
	if err != nil {
		return fmt.Errorf("remote storage put: %w", err)
	}
	buf := make([]byte, putChunkSize)
	first := true
	for {
		n, readErr := data.Read(buf)
		if n > 0 {
			req := &storagev1.PutRequest{Chunk: append([]byte(nil), buf[:n]...)}
			if first {
				req.Key = key
				req.TotalSize = size
				if size <= 0 {
					// unknown; announce 0 and let server use buffered length
					req.TotalSize = 0
				}
				first = false
			}
			if sendErr := stream.Send(req); sendErr != nil {
				return fmt.Errorf("remote storage put send: %w", sendErr)
			}
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return fmt.Errorf("remote storage put read: %w", readErr)
		}
	}
	if first {
		if sendErr := stream.Send(&storagev1.PutRequest{Key: key, TotalSize: size}); sendErr != nil {
			return fmt.Errorf("remote storage put empty: %w", sendErr)
		}
	}
	_, err = stream.CloseAndRecv()
	return err
}

func (p *Provider) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	stream, err := p.client.Get(ctx, &storagev1.GetRequest{Key: key})
	if err != nil {
		return nil, mapErr(err)
	}
	first, err := stream.Recv()
	if errors.Is(err, io.EOF) {
		return io.NopCloser(bytes.NewReader(nil)), nil
	}
	if err != nil {
		return nil, mapErr(err)
	}
	return &streamReader{first: first.GetChunk(), stream: stream}, nil
}

type chunkStream interface {
	Recv() (*storagev1.GetResponse, error)
}

//nolint:govet // fieldalignment: first buffer kept ahead of stream for hot path
type streamReader struct {
	first  []byte
	stream chunkStream
	err    error
}

func (r *streamReader) Read(p []byte) (int, error) {
	if r.err != nil {
		return 0, r.err
	}
	if len(r.first) > 0 {
		n := copy(p, r.first)
		r.first = r.first[n:]
		return n, nil
	}
	chunk, err := r.stream.Recv()
	if errors.Is(err, io.EOF) {
		r.err = io.EOF
		return 0, io.EOF
	}
	if err != nil {
		r.err = mapErr(err)
		return 0, r.err
	}
	r.first = chunk.GetChunk()
	n := copy(p, r.first)
	r.first = r.first[n:]
	return n, nil
}

func (r *streamReader) Close() error { return nil }

func (p *Provider) Stream(ctx context.Context, key string, offset, length int64) (io.ReadCloser, error) {
	stream, err := p.client.Get(ctx, &storagev1.GetRequest{Key: key, Offset: offset, Length: length})
	if err != nil {
		return nil, mapErr(err)
	}
	first, err := stream.Recv()
	if errors.Is(err, io.EOF) {
		return io.NopCloser(bytes.NewReader(nil)), nil
	}
	if err != nil {
		return nil, mapErr(err)
	}
	return &streamReader{first: first.GetChunk(), stream: stream}, nil
}

func (p *Provider) Delete(ctx context.Context, key string) error {
	_, err := p.client.Delete(ctx, &storagev1.DeleteRequest{Key: key})
	return mapErr(err)
}

func (p *Provider) Move(ctx context.Context, src, dst string) error {
	rc, err := p.Get(ctx, src)
	if err != nil {
		return err
	}
	defer func() { _ = rc.Close() }()
	body, err := io.ReadAll(rc)
	if err != nil {
		return err
	}
	if err := p.Put(ctx, dst, bytes.NewReader(body), int64(len(body))); err != nil {
		return err
	}
	return p.Delete(ctx, src)
}

func (p *Provider) Exists(ctx context.Context, key string) (bool, error) {
	resp, err := p.client.Stat(ctx, &storagev1.StatRequest{Key: key})
	if err != nil {
		if isNotFoundStatus(err) {
			return false, nil
		}
		return false, err
	}
	return resp.GetFound(), nil
}

func (p *Provider) Stat(ctx context.Context, key string) (contracts.ObjectInfo, error) {
	resp, err := p.client.Stat(ctx, &storagev1.StatRequest{Key: key})
	if err != nil {
		return contracts.ObjectInfo{}, mapErr(err)
	}
	if !resp.GetFound() {
		return contracts.ObjectInfo{}, contracts.ErrNotFound
	}
	return contracts.ObjectInfo{
		Key:          key,
		Size:         resp.GetSize(),
		ContentType:  resp.GetContentType(),
		LastModified: time.Unix(resp.GetLastModified(), 0).UTC(),
	}, nil
}

func (p *Provider) List(ctx context.Context, prefix string) ([]contracts.ObjectInfo, error) {
	resp, err := p.client.List(ctx, &storagev1.ListRequest{Prefix: prefix})
	if err != nil {
		return nil, err
	}
	out := make([]contracts.ObjectInfo, 0, len(resp.GetObjects()))
	for _, o := range resp.GetObjects() {
		out = append(out, contracts.ObjectInfo{
			Key:          o.GetKey(),
			Size:         o.GetSize(),
			ContentType:  o.GetContentType(),
			LastModified: time.Unix(o.GetLastModified(), 0).UTC(),
		})
	}
	return out, nil
}

func mapErr(err error) error {
	if err == nil {
		return nil
	}
	if isNotFoundStatus(err) {
		return contracts.ErrNotFound
	}
	return err
}

func isNotFoundStatus(err error) bool {
	st, ok := status.FromError(err)
	return ok && st.Code() == codes.NotFound
}

var (
	_ contracts.StorageProvider = (*Provider)(nil)
	_ contracts.Streamable      = (*Provider)(nil)
)
