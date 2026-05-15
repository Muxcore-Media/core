package api

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/Muxcore-Media/core/pkg/contracts"
)

type Server struct {
	http          *http.Server
	mux           *http.ServeMux
	healthChecker func() map[string]error
	AuthFunc      func(r *http.Request) (*contracts.Session, error)
	RateLimiter   *RateLimiter
}

func NewServer(addr string) *Server {
	mux := http.NewServeMux()
	s := &Server{mux: mux}

	mux.HandleFunc("/health", s.handleHealth)

	s.http = &http.Server{
		Addr:         addr,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}
	s.RateLimiter = NewRateLimiter(100, time.Minute) // 100 req/min default
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
// The returned map must contain all registered module IDs; nil values indicate healthy.
func (s *Server) SetHealthChecker(fn func() map[string]error) {
	s.healthChecker = fn
}

// SetAuthFunc sets the authentication function for the middleware chain.
// If fn is nil, auth is skipped (open mode). When called, the handler chain
// is rebuilt to include or exclude the auth middleware.
func (s *Server) SetAuthFunc(fn func(r *http.Request) (*contracts.Session, error)) {
	s.AuthFunc = fn
	s.rebuildChain()
}

// rebuildChain constructs the middleware chain in the correct order:
//  1. Recovery (innermost — catches panics from the mux)
//  2. Rate limit (if RateLimiter is set)
//  3. Auth (if AuthFunc is set)
//  4. Logging (outermost — logs all requests)
func (s *Server) rebuildChain() {
	var h http.Handler = s.mux
	h = recoveryMiddleware(h)
	if s.RateLimiter != nil {
		h = rateLimitMiddleware(s.RateLimiter)(h)
	}
	if s.AuthFunc != nil {
		h = authMiddleware(s.AuthFunc)(h)
	}
	h = withLogging(h)
	s.http.Handler = h
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// If health checker is set, include per-module health status
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
				w.Write([]byte(`<span class="inline-flex items-center gap-1.5">
	<span class="w-1.5 h-1.5 rounded-full bg-yellow-400"></span>
	System: Degraded
	</span>`))
			} else {
				w.Write([]byte(`<span class="inline-flex items-center gap-1.5">
	<span class="w-1.5 h-1.5 rounded-full bg-green-400"></span>
	System: Online
	</span>`))
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

	// No health checker — simple response
	if r.Header.Get("HX-Request") == "true" {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(`<span class="inline-flex items-center gap-1.5">
	<span class="w-1.5 h-1.5 rounded-full bg-green-400"></span>
	System: Online
	</span>`))
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
		slog.Info("request", "method", r.Method, "path", r.URL.Path, "duration", time.Since(start))
	})
}
