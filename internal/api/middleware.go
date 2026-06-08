package api

import (
	"context"
	"sync"
	"log/slog"
	"net/http"
	"runtime/debug"
	"strconv"
	"time"

	"github.com/Muxcore-Media/core/internal/trace"
	"github.com/Muxcore-Media/core/pkg/contracts"
	"github.com/google/uuid"
)

// contextKey is used for storing values in request context.
type contextKey string

// SessionKey is the context key for storing the authenticated session.
const SessionKey contextKey = "session"

// RoutePermission describes the permission required to access a route.
type RoutePermission struct {
	Action   string
	Resource string
}

// GetSession retrieves the authenticated session from the request context.
func GetSession(r *http.Request) (*contracts.Session, bool) {
	session, ok := r.Context().Value(SessionKey).(*contracts.Session)
	return session, ok
}

// authFailureRecord tracks authentication failures per IP for brute-force protection.
type authFailureRecord struct {
	count        int
	blockedUntil time.Time
	lastActivity time.Time
}

// recoveryMiddleware catches panics in downstream handlers, logs them, and returns 500.
func recoveryMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				slog.Error("panic recovered",
					"path", r.URL.Path,
					"method", r.Method,
					"error", rec,
					"stack", string(debug.Stack()),
				)
				http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// securityHeadersMiddleware sets standard security headers on all responses.
func securityHeadersMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
		w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		w.Header().Set("Strict-Transport-Security", "max-age=63072000; includeSubDomains; preload")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
		next.ServeHTTP(w, r)
	})
}

// authMiddleware returns a middleware that validates sessions using the provided function.
// Requests matching a path in publicPaths are always allowed through without authentication.
func authMiddleware(authFn func(r *http.Request) (*contracts.Session, error), publicPaths map[string]bool) func(http.Handler) http.Handler {
	// Per-IP auth failure tracking for brute-force protection
	authFailures := make(map[string]*authFailureRecord)
	var authFailMu sync.Mutex

	// Background cleanup goroutine to prevent unbounded map growth.
	// Purges entries with no activity for over 5 minutes.
	go func() {
		ticker := time.NewTicker(1 * time.Minute)
		defer ticker.Stop()
		for range ticker.C {
			authFailMu.Lock()
			cutoff := time.Now().Add(-5 * time.Minute)
			for ip, rec := range authFailures {
				if rec.lastActivity.Before(cutoff) {
					delete(authFailures, ip)
				}
			}
			authFailMu.Unlock()
		}
	}()

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Skip auth for public paths (e.g. /health).
			if publicPaths[r.URL.Path] {
				next.ServeHTTP(w, r)
				return
			}

			session, err := authFn(r)
			if err != nil {
				slog.Warn("auth failed", "error", err, "path", r.URL.Path, "remote_addr", r.RemoteAddr)
				// Track failures per IP for brute-force protection
				ip := extractClientIP(r)
				authFailMu.Lock()
				rec, exists := authFailures[ip]
				now := time.Now()
				if !exists {
					rec = &authFailureRecord{}
					authFailures[ip] = rec
				}
				rec.lastActivity = now
				if now.After(rec.blockedUntil) {
					rec.count++
					if rec.count >= 5 {
						rec.blockedUntil = now.Add(1 * time.Minute)
						rec.count = 0
					}
				}
				blocked := now.Before(rec.blockedUntil)
				authFailMu.Unlock()
				if blocked {
					writeJSON(w, http.StatusTooManyRequests, map[string]string{
						"error":   "rate_limited",
						"message": "too many authentication attempts, try again later",
					})
					return
				}
				writeJSON(w, http.StatusUnauthorized, map[string]string{
					"error":   "unauthorized",
					"message": "authentication failed",
				})
				return
			}

			ctx := context.WithValue(r.Context(), SessionKey, session)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// authzMiddleware returns a middleware that checks authorization for routes
// that have a required permission. The session must already be in the request
// context (placed by authMiddleware). Routes without an entry in routePerms
// are allowed through without authorization checks.
func authzMiddleware(authz contracts.Authorizer, routePerms map[string]RoutePermission) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			req, ok := routePerms[r.URL.Path]
			if !ok {
				// No permission required for this route.
				next.ServeHTTP(w, r)
				return
			}

			session, ok := GetSession(r)
			if !ok {
				writeJSON(w, http.StatusUnauthorized, map[string]string{
					"error":   "unauthorized",
					"message": "no session found",
				})
				return
			}

			allowed, err := authz.Can(r.Context(), *session, req.Action, req.Resource)
			if err != nil || !allowed {
				writeJSON(w, http.StatusForbidden, map[string]string{
					"error":   "forbidden",
					"message": "insufficient permissions",
				})
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// statusRecorder wraps http.ResponseWriter to capture the HTTP status code.
type statusRecorder struct {
	http.ResponseWriter
	statusCode int
}

func (sr *statusRecorder) WriteHeader(code int) {
	sr.statusCode = code
	sr.ResponseWriter.WriteHeader(code)
}

// auditMiddleware logs every authenticated request via the AuditLogger.
// It is placed after auth/authz in the middleware chain so it has access to
// the authenticated session. Public paths (e.g. /health) are skipped.
func auditMiddleware(auditLogger contracts.AuditLogger, publicPaths map[string]bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Skip public paths like /health.
			if publicPaths[r.URL.Path] {
				next.ServeHTTP(w, r)
				return
			}

			sr := &statusRecorder{ResponseWriter: w, statusCode: http.StatusOK}
			start := time.Now()
			next.ServeHTTP(sr, r)
			duration := time.Since(start)

			// Determine the actor from the authenticated session.
			actor := "anonymous"
			session, _ := GetSession(r)
			if session != nil {
				actor = session.UserID
			}

			entry := contracts.AuditEntry{
				ID:        uuid.New().String(),
				Timestamp: time.Now(),
				Actor:     actor,
				Action:    "http.request",
				Resource:  r.URL.Path,
				Details: map[string]string{
					"method":      r.Method,
					"status_code": strconv.Itoa(sr.statusCode),
					"ip":          extractClientIP(r),
					"duration_ms": strconv.FormatInt(duration.Milliseconds(), 10),
				},
				TraceID: trace.FromContext(r.Context()),
			}

			if session != nil {
				entry.Details["username"] = session.Username
			}

			// Log the audit entry; log errors but do not block the response.
			if err := auditLogger.Log(r.Context(), entry); err != nil {
				slog.Error("audit log failed", "error", err, "path", r.URL.Path)
			}
		})
	}
}

