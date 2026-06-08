package grpcmesh

import (
	"context"
	"net"
	"sync"
	"testing"
	"time"

	healthv1 "github.com/Muxcore-Media/core/proto/gen/muxcore/health/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
)

func TestRateLimitInterceptor_AllowsUnderLimit(t *testing.T) {
	t.Setenv("MUXCORE_GRPC_RATE_LIMIT", "100")
	rl := NewRateLimitInterceptor()
	defer rl.Close()

	// Under limit — should allow.
	for i := 0; i < 5; i++ {
		if !rl.allow("1.2.3.4") {
			t.Errorf("request %d should be allowed", i)
		}
	}
}

func TestRateLimitInterceptor_DeniesOverLimit(t *testing.T) {
	t.Setenv("MUXCORE_GRPC_RATE_LIMIT", "2")
	rl := NewRateLimitInterceptor()
	defer rl.Close()

	rl.allow("10.0.0.1")
	rl.allow("10.0.0.1")
	// Third request within one second should be denied.
	if rl.allow("10.0.0.1") {
		t.Error("third request within a second should be denied when limit is 2/min")
	}
}

func TestRateLimitInterceptor_DifferentIPs_Independent(t *testing.T) {
	t.Setenv("MUXCORE_GRPC_RATE_LIMIT", "1")
	rl := NewRateLimitInterceptor()
	defer rl.Close()

	rl.allow("1.1.1.1")
	// First request from a different IP should still be allowed.
	if !rl.allow("2.2.2.2") {
		t.Error("first request from different IP should be allowed")
	}
}

func TestRateLimitInterceptor_EvictIdle(t *testing.T) {
	t.Setenv("MUXCORE_GRPC_RATE_LIMIT", "10")
	rl := NewRateLimitInterceptor()
	defer rl.Close()

	rl.allow("3.3.3.3")

	// Manually age the bucket past the idle timeout.
	rl.mu.Lock()
	if b, ok := rl.buckets["3.3.3.3"]; ok {
		b.lastUsed = time.Now().Add(-11 * time.Minute)
	}
	rl.mu.Unlock()

	rl.evictIdle()

	rl.mu.Lock()
	_, exists := rl.buckets["3.3.3.3"]
	rl.mu.Unlock()
	if exists {
		t.Error("idle bucket should have been evicted")
	}
}

func TestRateLimitInterceptor_ConcurrentAccess(t *testing.T) {
	t.Setenv("MUXCORE_GRPC_RATE_LIMIT", "10000")
	rl := NewRateLimitInterceptor()
	defer rl.Close()

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rl.allow("4.4.4.4")
		}()
	}
	wg.Wait()
}

func TestRateLimitInterceptor_DefaultLimit(t *testing.T) {
	rl := NewRateLimitInterceptor()
	defer rl.Close()
	if rl.limit != defaultGRPCRateLimit {
		t.Errorf("expected default limit %d, got %d", defaultGRPCRateLimit, rl.limit)
	}
}

func TestRateLimitInterceptor_UnaryInterceptor_Wired(t *testing.T) {
	t.Setenv("MUXCORE_GRPC_RATE_LIMIT", "1000")
	rl := NewRateLimitInterceptor()
	defer rl.Close()

	// Start a real gRPC server with the rate limit interceptor.
	reg := NewHealthServer(nil) // use any gRPC service
	lis, _ := net.Listen("tcp", "127.0.0.1:0")
	srv := grpc.NewServer(
		grpc.ChainUnaryInterceptor(rl.UnaryInterceptor()),
	)
	healthv1.RegisterHealthServiceServer(srv, reg)
	go srv.Serve(lis)
	defer srv.GracefulStop()

	conn, _ := grpc.NewClient(lis.Addr().String(),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	defer conn.Close()

	client := healthv1.NewHealthServiceClient(conn)

	// Should succeed under the high limit.
	_, err := client.Check(context.Background(), &healthv1.HealthCheckRequest{})
	if err != nil {
		st, _ := status.FromError(err)
		if st.Code() == codes.ResourceExhausted {
			t.Error("request should not be rate-limited under high limit")
		}
		// Other errors (e.g. Unimplemented with nil registry) are OK here.
	}
}

func TestExtractGRPCClientIP_NoContext(t *testing.T) {
	ip := extractGRPCClientIP(context.Background())
	if ip == "" {
		t.Error("expected non-empty IP even without peer context")
	}
}
