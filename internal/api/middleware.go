package api

import (
	"context"
	"log/slog"
	"net/http"
	"runtime/debug"

	"github.com/Muxcore-Media/core/pkg/contracts"
)

// contextKey is used for storing values in request context.
type contextKey string

// SessionKey is the context key for storing the authenticated session.
const SessionKey contextKey = "session"

// GetSession retrieves the authenticated session from the request context.
func GetSession(r *http.Request) (*contracts.Session, bool) {
	session, ok := r.Context().Value(SessionKey).(*contracts.Session)
	return session, ok
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

// authMiddleware returns a middleware that validates sessions using the provided function.
// Requests to /health are always allowed through without authentication.
func authMiddleware(authFn func(r *http.Request) (*contracts.Session, error)) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Skip auth for health endpoint.
			if r.URL.Path == "/health" {
				next.ServeHTTP(w, r)
				return
			}

			session, err := authFn(r)
			if err != nil {
				writeJSON(w, http.StatusUnauthorized, map[string]string{
					"error":   "unauthorized",
					"message": err.Error(),
				})
				return
			}

			ctx := context.WithValue(r.Context(), SessionKey, session)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// chain composes multiple middleware functions around a final handler.
// Middleware are applied in order: the first middleware in the list wraps the
// outermost layer and executes first.
func chain(handler http.Handler, middlewares ...func(http.Handler) http.Handler) http.Handler {
	for i := len(middlewares) - 1; i >= 0; i-- {
		handler = middlewares[i](handler)
	}
	return handler
}
