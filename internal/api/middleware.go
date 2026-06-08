package api

import (
	"context"
	"net"
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
// When tlsActive is false, HSTS is omitted — emitting HSTS without TLS causes
// browsers to refuse plaintext connections for 2 years (CWE-523).
func securityHeadersMiddleware(next http.Handler, cspHeader string, tlsActive bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("X-XSS-Protection", "0")
		w.Header().Set("X-Permitted-Cross-Domain-Policies", "none")
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
		w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		w.Header().Set("Cache-Control", "no-store, max-age=0")
		if tlsActive {
			w.Header().Set("Strict-Transport-Security", "max-age=63072000; includeSubDomains; preload")
		}
		w.Header().Set("Content-Security-Policy", cspHeader)
		// CSRF protection: validate Origin header for state-changing methods.
		if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodOptions {
			if origin := r.Header.Get("Origin"); origin != "" && !isAllowedOrigin(origin) {
				w.Header().Set("Vary", "Origin")
				http.Error(w, "Forbidden", http.StatusForbidden)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// isAllowedOrigin checks whether an Origin header is allowed. By default,
// only same-origin requests (empty or missing Origin) are trusted. Modules
// that serve cross-origin browser content can register additional origins
// via SetTrustedOrigins.
var trustedOrigins []string

func isAllowedOrigin(origin string) bool {
	for _, t := range trustedOrigins {
		if origin == t {
			return true
		}
	}
	return false
}

// SetTrustedOrigins configures additional CORS origins for browser-based access.
// Empty list restricts to same-origin only (secure default).
func SetTrustedOrigins(origins []string) {
	trustedOrigins = origins
}

// auditSem limits concurrent audit goroutines from middleware to prevent
// unbounded goroutine bursts under heavy load or attack.
var auditSem = make(chan struct{}, 100)

// spawnFireAndForget launches a function in a goroutine bounded by auditSem.
func spawnFireAndForget(fn func()) {
	select {
	case auditSem <- struct{}{}:
		go func() {
			defer func() { <-auditSem }()
			fn()
		}()
	default:
		slog.Warn("audit: too many concurrent audit logs, dropping entry")
	}
}

// authMiddleware returns a middleware that validates sessions using the provided function.
// Requests matching a path in publicPaths are always allowed through without authentication.
// authFailures and authFailMu are shared with Server.authFailureCleanupLoop for periodic cleanup.
func authMiddleware(authFn func(r *http.Request) (*contracts.Session, error), auditLogger contracts.AuditLogger, publicPaths map[string]bool, trustedProxies []net.IPNet, authFailures map[string]*authFailureRecord, authFailMu *sync.Mutex) func(http.Handler) http.Handler {

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
				// Audit authentication failure.
				if auditLogger != nil {
					entry := contracts.AuditEntry{
						ID:        uuid.New().String(),
						Timestamp: time.Now(),
						Actor:     "anonymous",
						Action:    "auth.failure",
						Resource:  r.URL.Path,
						Details: map[string]string{
							"method": r.Method,
							"ip":     extractClientIP(r, trustedProxies),
							"error":  err.Error(),
						},
						TraceID: trace.FromContext(r.Context()),
					}
					spawnFireAndForget(func() {
						if err := auditLogger.Log(r.Context(), entry); err != nil {
							slog.Error("audit log write failed", "path", r.URL.Path, "error", err)
						}
					})
				}
				// Track failures per IP for brute-force protection
				ip := extractClientIP(r, trustedProxies)
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
func authzMiddleware(authz contracts.Authorizer, auditLogger contracts.AuditLogger, routePerms map[string]RoutePermission, trustedProxies []net.IPNet) func(http.Handler) http.Handler {
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

			// Try ResourceAuthorizer for ABAC support; fall back to flat Can.
			var allowed bool
			var err error
			if ra, ok2 := authz.(contracts.ResourceAuthorizer); ok2 {
				cleanPath := r.URL.Path
		allowed, err = ra.CanWithResource(r.Context(), *session, contracts.Action(req.Action),
					contracts.ResourceDescriptor{Type: req.Resource, ID: cleanPath})
			} else {
				allowed, err = authz.Can(r.Context(), *session, req.Action, req.Resource)
			}
			if err != nil || !allowed {
				// Audit authorization denial using Safe() session (no token).
				if auditLogger != nil {
					safeSession := session.Safe()
					errStr := ""
					if err != nil {
						errStr = err.Error()
					}
					entry := contracts.AuditEntry{
						ID:        uuid.New().String(),
						Timestamp: time.Now(),
						Actor:     safeSession.UserID,
						Action:    "authz.denied",
						Resource:  req.Resource,
						Details: map[string]string{
							"action":   req.Action,
							"username": safeSession.Username,
							"path":     r.URL.Path,
							"method":   r.Method,
							"error":    errStr,
						},
						TraceID: trace.FromContext(r.Context()),
					}
					spawnFireAndForget(func() {
						if err := auditLogger.Log(r.Context(), entry); err != nil {
							slog.Error("audit log write failed", "path", r.URL.Path, "error", err)
						}
					})
				}
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
func auditMiddleware(auditLogger contracts.AuditLogger, nodeID string, publicPaths map[string]bool, trustedProxies []net.IPNet) func(http.Handler) http.Handler {
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
					"ip":          extractClientIP(r, trustedProxies),
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

