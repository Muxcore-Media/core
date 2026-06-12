package grpcmesh

import (
	"context"
	"fmt"
	"time"

	cachev1 "github.com/Muxcore-Media/core/proto/gen/muxcore/cache/v1"
	"github.com/Muxcore-Media/core/pkg/contracts"
	"google.golang.org/grpc"
)

// SidecarCache wraps a gRPC connection to a sidecar module's CacheService
// and implements contracts.CacheProvider by forwarding over gRPC.
type SidecarCache struct {
	client cachev1.CacheServiceClient
}

// NewSidecarCache creates a CacheProvider backed by a sidecar module's gRPC CacheService.
func NewSidecarCache(conn *grpc.ClientConn) *SidecarCache {
	return &SidecarCache{
		client: cachev1.NewCacheServiceClient(conn),
	}
}

func (s *SidecarCache) Get(ctx context.Context, key string) ([]byte, error) {
	resp, err := s.client.Get(ctx, &cachev1.GetCacheRequest{Key: key})
	if err != nil {
		return nil, fmt.Errorf("sidecar cache: %w", err)
	}
	if !resp.GetFound() {
		return nil, nil
	}
	return resp.GetValue(), nil
}

func (s *SidecarCache) Set(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	_, err := s.client.Set(ctx, &cachev1.SetCacheRequest{
		Key:        key,
		Value:      value,
		TtlSeconds: int64(ttl.Seconds()),
	})
	if err != nil {
		return fmt.Errorf("sidecar cache: %w", err)
	}
	return nil
}

func (s *SidecarCache) Delete(ctx context.Context, keys ...string) error {
	_, err := s.client.Delete(ctx, &cachev1.DeleteCacheRequest{Keys: keys})
	if err != nil {
		return fmt.Errorf("sidecar cache: %w", err)
	}
	return nil
}

func (s *SidecarCache) Exists(ctx context.Context, key string) (bool, error) {
	resp, err := s.client.Exists(ctx, &cachev1.ExistsCacheRequest{Key: key})
	if err != nil {
		return false, fmt.Errorf("sidecar cache: %w", err)
	}
	return resp.GetExists(), nil
}

func (s *SidecarCache) Incr(ctx context.Context, key string, delta int64) (int64, error) {
	resp, err := s.client.Incr(ctx, &cachev1.IncrCacheRequest{Key: key, Delta: delta})
	if err != nil {
		return 0, fmt.Errorf("sidecar cache: %w", err)
	}
	return resp.GetValue(), nil
}

func (s *SidecarCache) CompareAndSwap(ctx context.Context, key string, oldValue, newValue []byte) (bool, error) {
	resp, err := s.client.CompareAndSwap(ctx, &cachev1.CompareAndSwapCacheRequest{
		Key:      key,
		OldValue: oldValue,
		NewValue: newValue,
	})
	if err != nil {
		return false, fmt.Errorf("sidecar cache: %w", err)
	}
	return resp.GetSwapped(), nil
}

type sidecarLock struct {
	client cachev1.CacheServiceClient
	key    string
	token  string
}

func (l *sidecarLock) Unlock(ctx context.Context) error {
	_, err := l.client.Unlock(ctx, &cachev1.UnlockCacheRequest{Key: l.key, Token: l.token})
	if err != nil {
		return fmt.Errorf("sidecar cache unlock: %w", err)
	}
	return nil
}

func (s *SidecarCache) Lock(ctx context.Context, key string, ttl time.Duration) (contracts.LockHandle, error) {
	resp, err := s.client.Lock(ctx, &cachev1.LockCacheRequest{
		Key:        key,
		TtlSeconds: int64(ttl.Seconds()),
	})
	if err != nil {
		return nil, fmt.Errorf("sidecar cache: %w", err)
	}
	if !resp.GetAcquired() {
		return nil, fmt.Errorf("sidecar cache: lock %q not acquired", key)
	}
	return &sidecarLock{client: s.client, key: key, token: resp.GetToken()}, nil
}

func (s *SidecarCache) Publish(ctx context.Context, channel string, msg []byte) error {
	_, err := s.client.Publish(ctx, &cachev1.PublishCacheRequest{
		Channel: channel,
		Message: msg,
	})
	if err != nil {
		return fmt.Errorf("sidecar cache: %w", err)
	}
	return nil
}

func (s *SidecarCache) Subscribe(ctx context.Context, channel string) (<-chan []byte, error) {
	stream, err := s.client.Subscribe(ctx, &cachev1.SubscribeCacheRequest{Channel: channel})
	if err != nil {
		return nil, fmt.Errorf("sidecar cache: %w", err)
	}
	ch := make(chan []byte, 256)
	go func() {
		defer close(ch)
		for {
			resp, err := stream.Recv()
			if err != nil {
				return
			}
			ch <- resp.GetMessage()
		}
	}()
	return ch, nil
}
