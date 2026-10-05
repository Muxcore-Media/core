package api

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"
)

// DefaultMetricsAddr is the default bind address of the dedicated metrics
// listener. It is loopback-only so metrics are never exposed by default.
const DefaultMetricsAddr = "127.0.0.1:9464"

// IsLoopbackAddr reports whether a host:port listen address binds only to a
// loopback interface. Empty hosts and unspecified addresses (":9464",
// "0.0.0.0:9464", "[::]:9464") bind all interfaces and are not loopback.
// Hostnames other than "localhost" are treated as non-loopback.
func IsLoopbackAddr(addr string) (bool, error) {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return false, fmt.Errorf("invalid listen address %q: %w", addr, err)
	}
	if host == "" {
		return false, nil
	}
	if strings.EqualFold(host, "localhost") {
		return true, nil
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false, nil
	}
	return ip.IsLoopback(), nil
}

// ValidateMetricsBind enforces NFR-SEC-011: a non-loopback metrics address
// requires a bearer token. It returns a clear error otherwise.
func ValidateMetricsBind(addr, token string) error {
	loop, err := IsLoopbackAddr(addr)
	if err != nil {
		return err
	}
	if !loop && token == "" {
		return fmt.Errorf("metrics: refusing to bind non-loopback address %q without authentication; "+
			"set MUXCORE_METRICS_TOKEN (or MUXCORE_METRICS_TOKEN_FILE) or use a loopback MUXCORE_METRICS_ADDR such as %s",
			addr, DefaultMetricsAddr)
	}
	return nil
}

// BearerAuth wraps next so requests must carry "Authorization: Bearer <token>".
// The comparison is constant-time. An empty token disables the check.
func BearerAuth(token string, next http.Handler) http.Handler {
	if token == "" {
		return next
	}
	want := sha256.Sum256([]byte(token))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got := ""
		if h := r.Header.Get("Authorization"); len(h) > 7 && strings.EqualFold(h[:7], "bearer ") {
			got = strings.TrimSpace(h[7:])
		}
		sum := sha256.Sum256([]byte(got))
		if subtle.ConstantTimeCompare(sum[:], want[:]) != 1 {
			w.Header().Set("WWW-Authenticate", `Bearer realm="muxcore-metrics"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// MetricsServer serves /metrics on a dedicated listener, separate from the
// main API server, so it can be bound to loopback by default.
type MetricsServer struct {
	http *http.Server
	ln   net.Listener
	auth bool
}

// NewMetricsServer validates the bind policy and binds addr (empty =
// DefaultMetricsAddr). When token is non-empty every request must present it
// as a bearer token. The listener is bound before returning so bind errors
// surface at startup. Call Serve to start handling requests.
func NewMetricsServer(addr, token string, p *MetricsProvider) (*MetricsServer, error) {
	if addr == "" {
		addr = DefaultMetricsAddr
	}
	if err := ValidateMetricsBind(addr, token); err != nil {
		return nil, err
	}
	var lc net.ListenConfig
	ln, err := lc.Listen(context.Background(), "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("metrics: listen %s: %w", addr, err)
	}
	mux := http.NewServeMux()
	mux.Handle("/metrics", BearerAuth(token, MetricsHandler(p)))
	return &MetricsServer{
		ln:   ln,
		auth: token != "",
		http: &http.Server{
			Handler:           mux,
			ReadHeaderTimeout: 5 * time.Second,
			ReadTimeout:       10 * time.Second,
			WriteTimeout:      10 * time.Second,
		},
	}, nil
}

// Addr returns the bound listener address.
func (m *MetricsServer) Addr() string { return m.ln.Addr().String() }

// Serve blocks serving requests until Shutdown is called.
func (m *MetricsServer) Serve() error {
	slog.Info("metrics endpoint listening", "addr", m.Addr(), "auth_required", m.auth)
	if err := m.http.Serve(m.ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// Shutdown gracefully stops the metrics listener.
func (m *MetricsServer) Shutdown(ctx context.Context) error { return m.http.Shutdown(ctx) }
