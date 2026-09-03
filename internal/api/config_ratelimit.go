package api

import (
	"net"
	"net/http"
	"strings"
)

// defaultConfigMutationRate is the sustained request rate for config mutations.
const defaultConfigMutationRate = 10

// defaultConfigMutationBurst is the burst allowance for config mutations.
const defaultConfigMutationBurst = 20

// isConfigMutation reports whether the request mutates configuration.
// Matches POST/PUT/DELETE/PATCH on paths containing "/config".
func isConfigMutation(r *http.Request) bool {
	switch r.Method {
	case http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodPatch:
	default:
		return false
	}
	path := r.URL.Path
	return strings.Contains(path, "/config")
}

// configMutationRateLimitMiddleware applies a stricter per-IP rate limit to
// config mutation endpoints (POST/PUT/DELETE/PATCH on /config paths).
func configMutationRateLimitMiddleware(limiter *DefaultRateLimiter, publicPaths map[string]bool, trustedProxies []net.IPNet) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if publicPaths[r.URL.Path] || !isConfigMutation(r) {
				next.ServeHTTP(w, r)
				return
			}
			ip := extractClientIP(r, trustedProxies)
			if !limiter.Allow(r.Context(), ip) {
				w.Header().Set("Retry-After", "60")
				writeJSON(w, http.StatusTooManyRequests, map[string]string{
					"error":   "rate_limited",
					"message": "too many config mutation requests, try again later",
				})
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
