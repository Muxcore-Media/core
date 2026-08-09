//nolint:govet // struct field alignment
package api

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Muxcore-Media/core/internal/config"
	"github.com/Muxcore-Media/core/internal/trace"
	"github.com/Muxcore-Media/core/internal/version"
	"github.com/Muxcore-Media/core/pkg/contracts"
)

const (
	headerXForwardedFor = "X-Forwarded-For"
	headerXRealIP       = "X-Real-IP"
	headerHXRequest     = "HX-Request"
)

type Server struct {
	http             *http.Server
	mux              *http.ServeMux
	healthChecker    func() map[string]error
	AuthFunc         func(r *http.Request) (*contracts.Session, error)
	rateLimiter      contracts.RateLimiterProvider
	authorizer       contracts.Authorizer
	auditLogger      contracts.AuditLogger
	nodeID           string
	routePermissions map[string]RoutePermission
	publicPaths      map[string]bool
	certFile         string
	keyFile          string
	// cspHeader is the Content-Security-Policy header value set on all responses.
	// Default is restrictive: "default-src 'none'; frame-ancestors 'none'".
	// Modules serving HTML content (e.g. admin UI) must call SetCSP with
	// appropriate directives for script-src, style-src, img-src, connect-src.
	cspHeader string

	// authFailureCleanup stops the background auth failure map cleanup ticker.
	authFailureCleanup chan struct{}
	// authFailureMu guards the authFailures map for the cleanup loop.
	authFailureMu     sync.Mutex
	authFailures      map[string]*authFailureRecord
	authFailuresTotal atomic.Int64
	requestCount      atomic.Int64
	statusHTTP2xx     atomic.Int64
	statusHTTP3xx     atomic.Int64
	statusHTTP4xx     atomic.Int64
	statusHTTP5xx     atomic.Int64
	// trustedProxies is the list of CIDR ranges whose X-Forwarded-For we trust.
	trustedProxies []net.IPNet
	// trustedOrigins restricts CORS Origin headers that are accepted for
	// state-changing requests. Empty means same-origin only (secure default).
	trustedOrigins []string
}

func NewServer(addr, certFile, keyFile string) *Server {
	mux := http.NewServeMux()
	s := &Server{
		mux:                mux,
		publicPaths:        map[string]bool{"/health": true, "/version": true},
		cspHeader:          "default-src 'none'; frame-ancestors 'none'",
		routePermissions:   make(map[string]RoutePermission),
		authFailureCleanup: make(chan struct{}),
		authFailures:       make(map[string]*authFailureRecord),
		trustedProxies:     []net.IPNet{{IP: net.IPv4(127, 0, 0, 0), Mask: net.CIDRMask(8, 32)}, {IP: net.ParseIP("::1"), Mask: net.CIDRMask(128, 128)}},
		rateLimiter:        NewDefaultRateLimiter(100, 200),
	}
	// Start auth failure cleanup ticker once, not per rebuildChain call.
	go s.authFailureCleanupLoop()

	mux.HandleFunc("/health", s.handleHealth)
	mux.HandleFunc("/version", s.handleVersion)

	s.http = &http.Server{
		Addr:           addr,
		ReadTimeout:    15 * time.Second,
		WriteTimeout:   15 * time.Second,
		IdleTimeout:    120 * time.Second,
		MaxHeaderBytes: 65536, // 64 KB
		TLSConfig: &tls.Config{
			MinVersion:               tls.VersionTLS12,
			PreferServerCipherSuites: true,
			CipherSuites: []uint16{
				tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,
				tls.TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384,
				tls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256,
				tls.TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384,
			},
		},
	}
	s.rebuildChain()
	s.certFile = certFile
	s.keyFile = keyFile
	if certFile != "" && keyFile != "" {
		slog.Info("API server TLS enabled", "cert", certFile, "key", keyFile)
	}
	return s
}

func (s *Server) Start() error {
	slog.Info("API server listening", "addr", s.http.Addr)
	if s.certFile != "" && s.keyFile != "" {
		return s.http.ListenAndServeTLS(s.certFile, s.keyFile)
	}
	if devTLSSkipCheck() {
		slog.Warn("API server starting without TLS — insecure mode explicitly enabled")
		return s.http.ListenAndServe()
	}
	return fmt.Errorf("TLS is required — set MUXCORE_SERVER_TLS_CERT and MUXCORE_SERVER_TLS_KEY env vars, or MUXCORE_DEV_TLS_SKIP=true for development")
}

// Drain signals the HTTP server to stop accepting new connections and waits
// for in-flight requests to complete within the given context deadline.
// After Drain returns, no new requests will be processed. Call Shutdown
// afterward for final cleanup.
//
// Typical shutdown sequence:
//  1. srv.Drain(drainCtx)    — stop new connections, drain in-flight
//  2. grpcSrv.GracefulStop() — drain gRPC
//  3. modMgr.StopAll(...)    — stop modules
//  4. srv.Shutdown(ctx)      — release remaining HTTP resources
func (s *Server) Drain(ctx context.Context) error {
	slog.Info("API server draining — stopping new connections")
	// http.Server.Shutdown gracefully stops the server: closes the listener
	// immediately (no new connections accepted) and waits for active
	// connections to finish. This is exactly what "drain" means.
	return s.http.Shutdown(ctx)
}

func (s *Server) Shutdown(ctx context.Context) error {
	close(s.authFailureCleanup)
	// After Drain, the server is already shut down. This is a no-op but
	// harmless — Shutdown on an already-shut-down server returns nil.
	return s.http.Shutdown(ctx)
}

// AuthFailuresTotalCount returns the total number of auth failures recorded.
func (s *Server) AuthFailuresTotalCount() int64 {
	return s.authFailuresTotal.Load()
}

// BackoffActiveCount returns the number of IPs currently in auth backoff.
func (s *Server) BackoffActiveCount() int {
	s.authFailureMu.Lock()
	defer s.authFailureMu.Unlock()
	now := time.Now()
	count := 0
	for _, rec := range s.authFailures {
		if now.Before(rec.blockedUntil) {
			count++
		}
	}
	return count
}

// RequestCount returns the total number of HTTP requests processed.
func (s *Server) RequestCount() int64 {
	return s.requestCount.Load()
}

func (s *Server) StatusHTTP2xx() int64 { return s.statusHTTP2xx.Load() }
func (s *Server) StatusHTTP3xx() int64 { return s.statusHTTP3xx.Load() }
func (s *Server) StatusHTTP4xx() int64 { return s.statusHTTP4xx.Load() }
func (s *Server) StatusHTTP5xx() int64 { return s.statusHTTP5xx.Load() }

// authFailureCleanupLoop periodically purges stale auth failure records.
// Entries with no activity for over 10 minutes are removed.
func (s *Server) authFailureCleanupLoop() {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-s.authFailureCleanup:
			return
		case <-ticker.C:
			s.authFailureMu.Lock()
			cutoff := time.Now().Add(-10 * time.Minute)
			for ip, rec := range s.authFailures {
				if rec.lastActivity.Before(cutoff) {
					delete(s.authFailures, ip)
				}
			}
			s.authFailureMu.Unlock()
		}
	}
}

// SetTrustedProxies configures CIDR ranges whose X-Forwarded-For headers are
// trusted. By default only loopback (127.0.0.0/8, ::1/128) is trusted.
// Pass nil to trust only loopback. Pass an empty-but-initialized slice to
// trust no proxy at all (X-Forwarded-For is ignored entirely).
func (s *Server) SetTrustedProxies(cidrs []string) {
	if len(cidrs) == 0 {
		s.trustedProxies = []net.IPNet{{IP: net.IPv4(127, 0, 0, 0), Mask: net.CIDRMask(8, 32)}, {IP: net.ParseIP("::1"), Mask: net.CIDRMask(128, 128)}}
		return
	}
	parsed := make([]net.IPNet, 0, len(cidrs))
	for _, c := range cidrs {
		_, n, err := net.ParseCIDR(c)
		if err != nil {
			slog.Warn("ignoring invalid trusted proxy CIDR", "cidr", c, "error", err)
			continue
		}
		parsed = append(parsed, *n)
	}
	if len(parsed) == 0 {
		parsed = append(parsed, net.IPNet{IP: net.IPv4(127, 0, 0, 0), Mask: net.CIDRMask(8, 32)})
	}
	parsed = append(parsed, net.IPNet{IP: net.ParseIP("::1"), Mask: net.CIDRMask(128, 128)})
	s.trustedProxies = parsed
}

// SetTrustedOrigins configures additional CORS origins for browser-based access.
// Empty list restricts to same-origin only (secure default).
func (s *Server) SetTrustedOrigins(origins []string) {
	s.trustedOrigins = origins
}

// Handle registers an http.Handler for the given pattern.
func (s *Server) Handle(pattern string, handler http.Handler) {
	s.mux.Handle(pattern, handler)
}

// HandleFunc registers a handler function for the given pattern.
func (s *Server) HandleFunc(pattern string, handler func(http.ResponseWriter, *http.Request)) {
	s.mux.HandleFunc(pattern, handler)
}

// SetHealthChecker sets a function that returns per-module health status.
func (s *Server) SetHealthChecker(fn func() map[string]error) {
	s.healthChecker = fn
}

// SetAuthFunc sets the authentication function for the middleware chain.
func (s *Server) SetAuthFunc(fn func(r *http.Request) (*contracts.Session, error)) {
	s.AuthFunc = fn
	s.rebuildChain()
}

// SetCSP configures the Content-Security-Policy header sent on all responses.
// The default is restrictive: "default-src 'none'; frame-ancestors 'none'".
// Modules that serve HTML content (e.g. admin UI) must call this to set
// appropriate script-src, style-src, connect-src, and img-src directives.
func (s *Server) SetCSP(header string) {
	s.cspHeader = header
}

// SetRateLimiter sets the rate limiter module for the middleware chain.
func (s *Server) SetRateLimiter(rl contracts.RateLimiterProvider) {
	s.rateLimiter = rl
	s.rebuildChain()
}

// SetAuthorizer sets the authorizer for permission checks in the middleware chain.
func (s *Server) SetAuthorizer(a contracts.Authorizer) {
	s.authorizer = a
	s.rebuildChain()
}

// SetAuditLogger sets the audit logger for recording authenticated requests.
func (s *Server) SetAuditLogger(a contracts.AuditLogger) {
	s.auditLogger = a
	s.rebuildChain()
}

// SetNodeID sets the node identifier for audit entries.
func (s *Server) SetNodeID(id string) {
	s.nodeID = id
}

// RouteRequire registers a permission requirement for a specific route.
// The route must match an HTTP path pattern registered with Handle/HandleFunc.
// The authorizer's Can() method will be called with the given action and resource
// after authentication for all requests to this route.
func (s *Server) RouteRequire(pattern, action, resource string) {
	s.routePermissions[pattern] = RoutePermission{Action: action, Resource: resource}
	s.rebuildChain()
}

// AddPublicPath adds a path that should skip authentication entirely.
// Requests to public paths are not required to carry a valid session.
func (s *Server) AddPublicPath(path string) {
	s.publicPaths[path] = true
	s.rebuildChain()
}

// rebuildChain constructs the middleware chain.
// Order: MaxBytesReader (outermost) → security headers → recovery → rate limit → auth → authz → audit → logging → trace.
// In Go HTTP middleware, the last wrapper applied executes first, so the build
// order is the reverse of the execution order.
func (s *Server) rebuildChain() {
	tlsActive := (s.certFile != "" && s.keyFile != "") || !devTLSSkipCheck()

	var h http.Handler = s.mux
	trusted := replicateSlice(s.trustedProxies)
	// Build from innermost to outermost:
	// trace (innermost, executes last before handler)
	h = trace.HTTPMiddleware(h)
	h = withLogging(h, &s.requestCount)
	if s.auditLogger != nil {
		statusCnt := &[6]*atomic.Int64{
			1: &s.statusHTTP2xx,
			2: &s.statusHTTP3xx,
			3: &s.statusHTTP4xx,
			4: &s.statusHTTP5xx,
		}
		h = auditMiddleware(s.auditLogger, s.nodeID, s.publicPaths, trusted, statusCnt)(h)
	}
	if s.authorizer != nil {
		h = authzMiddleware(s.authorizer, s.auditLogger, s.routePermissions, trusted)(h)
	}
	if s.AuthFunc != nil {
		h = authMiddleware(s.AuthFunc, s.auditLogger, s.publicPaths, trusted, s.authFailures, &s.authFailureMu, &s.authFailuresTotal)(h)
	}
	if s.rateLimiter != nil {
		h = rateLimitMiddleware(s.rateLimiter, s.publicPaths, trusted)(h)
	}
	h = recoveryMiddleware(h)
	h = securityHeadersMiddleware(h, s.cspHeader, tlsActive, s.trustedOrigins)
	h = maxBodyMiddleware(h)
	s.http.Handler = h
}

// handleHealth serves the /health endpoint.
//
// Method: GET only; other methods return 405.
//
// Response (JSON, Content-Type: application/json):
//
//	When no health checker is registered:
//	  {"status": "ok", "time": "2026-06-08T12:00:00Z"}
//
//	When a health checker is registered:
//	  {"status": "ok"|"degraded", "time": "...", "modules": {"module_id": "ok"|"error"}}
//
//	HTTP status: 200 when healthy, 503 when any module reports an error.
//
// HTMX support: when the HX-Request header is "true", returns an HTML snippet
// with a colored status indicator instead of JSON. This enables live-updating
// health badges in HTMX-driven admin UIs without client-side JSON parsing.
//
// The /health path is public (no authentication required) by default, set in
// the publicPaths map during NewServer.
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if s.healthChecker != nil {
		moduleHealth := s.healthChecker()
		degraded := false
		modules := make(map[string]string, len(moduleHealth))
		for id, err := range moduleHealth {
			// Keys prefixed with "_" are informational metadata (uptime, version),
			// not failure probes — include them without marking the system degraded.
			if id != "" && id[0] == '_' {
				if err != nil {
					modules[id] = err.Error()
				} else {
					modules[id] = "ok"
				}
				continue
			}
			if err != nil {
				modules[id] = "error"
				degraded = true
			} else {
				modules[id] = "ok"
			}
		}

		status := "ok"
		httpStatus := http.StatusOK
		if degraded {
			status = "degraded"
			httpStatus = http.StatusServiceUnavailable
		}

		if r.Header.Get(headerHXRequest) == "true" {
			w.Header().Set("Content-Type", "text/html")
			if degraded {
				if _, err := w.Write([]byte(`<span class="inline-flex items-center gap-1.5"><span class="w-1.5 h-1.5 rounded-full bg-yellow-400"></span>System: Degraded</span>`)); err != nil {
					slog.Debug("health htmx degraded write", "error", err)
				}
			} else {
				if _, err := w.Write([]byte(`<span class="inline-flex items-center gap-1.5"><span class="w-1.5 h-1.5 rounded-full bg-green-400"></span>System: Online</span>`)); err != nil {
					slog.Debug("health htmx online write", "error", err)
				}
			}
			return
		}

		writeJSON(w, httpStatus, map[string]any{
			"status":  status,
			"time":    time.Now().UTC().Format(time.RFC3339),
			"modules": modules,
		})
		return
	}

	if r.Header.Get(headerHXRequest) == "true" {
		w.Header().Set("Content-Type", "text/html")
		if _, err := w.Write([]byte(`<span class="inline-flex items-center gap-1.5"><span class="w-1.5 h-1.5 rounded-full bg-green-400"></span>System: Online</span>`)); err != nil {
			slog.Debug("health htmx write", "error", err)
		}
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{
		"status": "ok",
		"time":   time.Now().UTC().Format(time.RFC3339),
	})
}

func (s *Server) handleVersion(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{
		"version": version.String(),
	})
}

// devTLSSkipCheck returns true when TLS enforcement should be bypassed.
// Delegates to the shared config function which checks both
// MUXCORE_INSECURE_DISABLE_TLS (canonical) and the deprecated
// MUXCORE_DEV_TLS_SKIP.
func devTLSSkipCheck() bool {
	return config.InsecureTLSSkipEnabled()
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Error("writeJSON: encode failed", "error", err)
	}
}

func withLogging(next http.Handler, reqCounter *atomic.Int64) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if reqCounter != nil {
			reqCounter.Add(1)
		}
		start := time.Now()
		next.ServeHTTP(w, r)
		slog.Info("request", "method", r.Method, "path", r.URL.Path, "duration", time.Since(start), "trace_id", trace.FromContext(r.Context()))
	})
}

func rateLimitMiddleware(limiter contracts.RateLimiterProvider, publicPaths map[string]bool, trustedProxies []net.IPNet) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if publicPaths[r.URL.Path] {
				next.ServeHTTP(w, r)
				return
			}
			ip := extractClientIP(r, trustedProxies)
			if !limiter.Allow(r.Context(), ip) {
				w.Header().Set("Retry-After", "60")
				writeJSON(w, http.StatusTooManyRequests, map[string]string{
					"error":   "rate_limited",
					"message": "too many requests, try again later",
				})
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func replicateSlice(src []net.IPNet) []net.IPNet {
	dst := make([]net.IPNet, len(src))
	copy(dst, src)
	return dst
}

func isTrustedProxy(addr string, trustedProxies []net.IPNet) bool {
	ip := net.ParseIP(addr)
	if ip == nil {
		return false
	}
	for _, n := range trustedProxies {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

func extractClientIP(r *http.Request, trustedProxies []net.IPNet) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	if isTrustedProxy(host, trustedProxies) {
		if xff := r.Header.Get(headerXForwardedFor); xff != "" {
			if ip := parseRightmostXFF(xff); ip != "" {
				return ip
			}
		}
		if xri := r.Header.Get(headerXRealIP); xri != "" {
			if ip := net.ParseIP(xri); ip != nil {
				return ip.String()
			}
		}
	}
	return host
}

// parseRightmostXFF extracts and validates the rightmost IP from an
// X-Forwarded-For header value. Returns the IP string, or "" if invalid.
// maxBodySize is the maximum HTTP request body size (10 MB), enforced via
// http.MaxBytesReader to prevent memory exhaustion attacks (CWE-770).
const maxBodySize = 10 << 20

// maxBodyMiddleware wraps the handler with http.MaxBytesReader to enforce
// a maximum request body size. Requests exceeding the limit receive a
// 413 Payload Too Large response.
func maxBodyMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, maxBodySize)
		next.ServeHTTP(w, r)
	})
}

func parseRightmostXFF(xff string) string {
	// Split on commas; the rightmost entry is the immediate upstream proxy.
	parts := strings.Split(xff, ",")
	if len(parts) == 0 {
		return ""
	}
	rightmost := strings.TrimSpace(parts[len(parts)-1])
	if rightmost == "" {
		return ""
	}
	if ip := net.ParseIP(rightmost); ip != nil {
		return ip.String()
	}
	return ""
}
