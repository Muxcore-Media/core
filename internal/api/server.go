package api

import (
	"context"
	"encoding/json"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/Muxcore-Media/core/internal/trace"
	"github.com/Muxcore-Media/core/pkg/contracts"
)

type Server struct {
	http             *http.Server
	mux              *http.ServeMux
	healthChecker    func() map[string]error
	AuthFunc         func(r *http.Request) (*contracts.Session, error)
	rateLimiter      contracts.RateLimiterProvider
	authorizer       contracts.Authorizer
	auditLogger      contracts.AuditLogger
	routePermissions map[string]RoutePermission
	publicPaths      map[string]bool
}

func NewServer(addr string) *Server {
	mux := http.NewServeMux()
	s := &Server{
		mux:              mux,
		publicPaths:      map[string]bool{"/health": true},
		routePermissions: make(map[string]RoutePermission),
	}

	mux.HandleFunc("/health", s.handleHealth)

	s.http = &http.Server{
		Addr:         addr,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}
	s.rebuildChain()
	return s
}

func (s *Server) Start() error {
	slog.Info("API server listening", "addr", s.http.Addr)
	return s.http.ListenAndServe()
}

func (s *Server) Shutdown(ctx context.Context) error {
	return s.http.Shutdown(ctx)
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
func (s *Server) rebuildChain() {
	var h http.Handler = s.mux
	h = recoveryMiddleware(h)
	if s.rateLimiter != nil && s.rateLimiter.Enabled() {
		h = rateLimitMiddleware(s.rateLimiter, s.publicPaths)(h)
	}
	if s.AuthFunc != nil {
		h = authMiddleware(s.AuthFunc, s.publicPaths)(h)
	}
	if s.authorizer != nil {
		h = authzMiddleware(s.authorizer, s.routePermissions)(h)
	}
	if s.auditLogger != nil {
		h = auditMiddleware(s.auditLogger, s.publicPaths)(h)
	}
	h = withLogging(h)
	h = trace.HTTPMiddleware(h)
	s.http.Handler = h
}

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
			if err != nil {
				modules[id] = err.Error()
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

		if r.Header.Get("HX-Request") == "true" {
			w.Header().Set("Content-Type", "text/html")
			if degraded {
				w.Write([]byte(`<span class="inline-flex items-center gap-1.5"><span class="w-1.5 h-1.5 rounded-full bg-yellow-400"></span>System: Degraded</span>`))
			} else {
				w.Write([]byte(`<span class="inline-flex items-center gap-1.5"><span class="w-1.5 h-1.5 rounded-full bg-green-400"></span>System: Online</span>`))
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

	if r.Header.Get("HX-Request") == "true" {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(`<span class="inline-flex items-center gap-1.5"><span class="w-1.5 h-1.5 rounded-full bg-green-400"></span>System: Online</span>`))
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{
		"status": "ok",
		"time":   time.Now().UTC().Format(time.RFC3339),
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func withLogging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		slog.Info("request", "method", r.Method, "path", r.URL.Path, "duration", time.Since(start), "trace_id", trace.FromContext(r.Context()))
	})
}

func rateLimitMiddleware(limiter contracts.RateLimiterProvider, publicPaths map[string]bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if publicPaths[r.URL.Path] {
				next.ServeHTTP(w, r)
				return
			}
			ip := extractClientIP(r)
			if !limiter.Allow(ip) {
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

func extractClientIP(r *http.Request) string {
	// Only trust the rightmost IP in X-Forwarded-For (the immediate upstream proxy).
	// Validate it with net.ParseIP to prevent IP spoofing for rate limit bypass.
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		if ip := parseRightmostXFF(xff); ip != "" {
			return ip
		}
	}
	if xri := r.Header.Get("X-Real-IP"); xri != "" {
		if ip := net.ParseIP(xri); ip != nil {
			return ip.String()
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// parseRightmostXFF extracts and validates the rightmost IP from an
// X-Forwarded-For header value. Returns the IP string, or "" if invalid.
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
