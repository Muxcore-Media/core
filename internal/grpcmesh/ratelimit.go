package grpcmesh

import (
	"context"
	"log/slog"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
)

const (
	// defaultGRPCRateLimit is the default max requests per minute per IP.
	defaultGRPCRateLimit = 1000

	// bucketCleanupInterval is how often idle IP buckets are evicted.
	bucketCleanupInterval = 5 * time.Minute

	// bucketIdleTimeout is how long an IP can be idle before its bucket is removed.
	bucketIdleTimeout = 10 * time.Minute
)

// tokenBucket implements a simple token bucket for rate limiting.
type tokenBucket struct {
	tokens   float64
	capacity float64
	refillPS float64 // tokens per second
	lastTime time.Time
	lastUsed time.Time
}

func newTokenBucket(ratePerMinute int) *tokenBucket {
	cap := float64(ratePerMinute)
	return &tokenBucket{
		tokens:   cap,
		capacity: cap,
		refillPS: cap / 60.0,
		lastTime: time.Now(),
		lastUsed: time.Now(),
	}
}

// Allow returns true if a token is available and consumes it.
func (b *tokenBucket) Allow() bool {
	now := time.Now()
	elapsed := now.Sub(b.lastTime).Seconds()
	b.tokens = min(b.capacity, b.tokens+elapsed*b.refillPS)
	b.lastTime = now
	b.lastUsed = now

	if b.tokens >= 1 {
		b.tokens--
		return true
	}
	return false
}

func min(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}

// RateLimitInterceptor enforces per-IP request rate limiting on gRPC calls.
// The limit is configured via MUXCORE_GRPC_RATE_LIMIT (requests per minute).
// When 0 or unset, the default (1000 req/min) is used.
type RateLimitInterceptor struct {
	mu       sync.Mutex
	buckets  map[string]*tokenBucket
	limit    int
	stopCh   chan struct{}
}

// NewRateLimitInterceptor creates a rate limiter. Call Close() on shutdown.
func NewRateLimitInterceptor() *RateLimitInterceptor {
	limit := defaultGRPCRateLimit
	if v := os.Getenv("MUXCORE_GRPC_RATE_LIMIT"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}

	rl := &RateLimitInterceptor{
		buckets: make(map[string]*tokenBucket),
		limit:   limit,
		stopCh:  make(chan struct{}),
	}
	go rl.cleanupLoop()
	slog.Info("gRPC rate limiter enabled", "limit_per_minute_per_ip", limit)
	return rl
}

// UnaryInterceptor returns a gRPC unary server interceptor that enforces the rate limit.
func (rl *RateLimitInterceptor) UnaryInterceptor() grpc.UnaryServerInterceptor {
	return func(
		ctx context.Context,
		req interface{},
		info *grpc.UnaryServerInfo,
		handler grpc.UnaryHandler,
	) (interface{}, error) {
		ip := extractGRPCClientIP(ctx)
		if !rl.allow(ip) {
			slog.Warn("gRPC rate limit exceeded", "ip", ip, "method", info.FullMethod)
			return nil, status.Errorf(codes.ResourceExhausted,
				"rate limit exceeded: max %d requests/minute per IP", rl.limit)
		}
		return handler(ctx, req)
	}
}

// StreamInterceptor returns a gRPC streaming server interceptor.
func (rl *RateLimitInterceptor) StreamInterceptor() grpc.StreamServerInterceptor {
	return func(
		srv interface{},
		ss grpc.ServerStream,
		info *grpc.StreamServerInfo,
		handler grpc.StreamHandler,
	) error {
		ip := extractGRPCClientIP(ss.Context())
		if !rl.allow(ip) {
			slog.Warn("gRPC rate limit exceeded (stream)", "ip", ip, "method", info.FullMethod)
			return status.Errorf(codes.ResourceExhausted,
				"rate limit exceeded: max %d requests/minute per IP", rl.limit)
		}
		return handler(srv, ss)
	}
}

func (rl *RateLimitInterceptor) allow(ip string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	bucket, ok := rl.buckets[ip]
	if !ok {
		bucket = newTokenBucket(rl.limit)
		rl.buckets[ip] = bucket
	}
	return bucket.Allow()
}

// Close stops the cleanup goroutine.
func (rl *RateLimitInterceptor) Close() {
	close(rl.stopCh)
}

func (rl *RateLimitInterceptor) cleanupLoop() {
	ticker := time.NewTicker(bucketCleanupInterval)
	defer ticker.Stop()
	for {
		select {
		case <-rl.stopCh:
			return
		case <-ticker.C:
			rl.evictIdle()
		}
	}
}

func (rl *RateLimitInterceptor) evictIdle() {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	cutoff := time.Now().Add(-bucketIdleTimeout)
	for ip, b := range rl.buckets {
		if b.lastUsed.Before(cutoff) {
			delete(rl.buckets, ip)
		}
	}
}

// extractGRPCClientIP extracts the client IP from gRPC peer context.
func extractGRPCClientIP(ctx context.Context) string {
	p, ok := peer.FromContext(ctx)
	if !ok {
		return "unknown"
	}
	addr := p.Addr.String()
	// Strip port if present.
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		// May already be just an IP.
		return strings.TrimSpace(addr)
	}
	return host
}
