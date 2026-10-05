//nolint:govet // struct field alignment
package grpcmesh

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/Muxcore-Media/core/internal/callerid"
	"github.com/Muxcore-Media/core/internal/config"
	"github.com/Muxcore-Media/core/internal/trace"
	"github.com/Muxcore-Media/core/pkg/contracts"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
)

// authFailureRecord tracks gRPC authentication failures per caller identity
// for brute-force protection, analogous to the HTTP auth failure tracking.
// Uses cumulative counts with exponential backoff.
type grpcAuthFailureRecord struct { //nolint:govet // struct field alignment is acceptable
	count         int
	totalFailures int
	blockedUntil  time.Time
	lastActivity  time.Time
}

const (
	gRPCAuthBackoffThreshold = 6
	gRPCAuthBackoffDuration  = 1 * time.Minute
	gRPCAuthMaxBackoff       = 60 * time.Minute
	gRPCAuthDecayLimit       = 100 // permanent lockout after this many total failures
)

// moduleRegistrationMethods lists gRPC methods that do not require
// authentication — sidecar modules must be able to register before they
// have an established identity.
var moduleRegistrationMethods = map[string]bool{
	"/muxcore.module.v1.ModuleRegistration/Register":          true,
	"/muxcore.module.v1.ModuleRegistration/Unregister":        true,
	"/muxcore.module.v1.ModuleRegistration/BootstrapRegister": true, // one-time token is the auth
	"/muxcore.discovery.v1.DiscoveryService/FindByCapability": true,
	"/muxcore.discovery.v1.DiscoveryService/FindById":         true,
	"/muxcore.discovery.v1.DiscoveryService/FindByRole":       true,
	"/muxcore.discovery.v1.DiscoveryService/List":             true, // legacy alias name
	"/muxcore.discovery.v1.DiscoveryService/ListAll":          true,
	"/muxcore.discovery.v1.DiscoveryService/Members":          true,
	"/muxcore.discovery.v1.DiscoveryService/ListMembers":      true,
	"/muxcore.discovery.v1.DiscoveryService/Watch":            true,
	"/muxcore.discovery.v1.DiscoveryService/Resolve":          true,
	"/muxcore.discovery.v1.DiscoveryService/Join":             true, // join-token metadata is the auth
	"/muxcore.discovery.v1.DiscoveryService/Heartbeat":        true, // cluster liveness without Authorizer
	"/muxcore.events.v1.EventService/Subscribe":               true,
	"/muxcore.events.v1.EventService/Unsubscribe":             true,
	"/muxcore.events.v1.EventService/GetStats":                true,
	// Publish requires verified identity via the auth interceptor; publish-policy
	// enforces caller authorization at the event bus layer.
	"/muxcore.health.v1.HealthService/Check": true,
}

// AuthInterceptor provides gRPC unary and stream interceptors that enforce
// caller authentication and authorization.
//
// Behavior by configuration:
//   - Authorizer + IdentityProvider set: full authentication + authorization.
//   - Only Authorizer set (no IdentityProvider): returns Unavailable (misconfiguration).
//   - Neither set: denies all calls except those in moduleRegistrationMethods.
//   - ModuleRegistration service is always open (modules must register before
//     they have an identity).
type AuthInterceptor struct {
	mu               sync.RWMutex
	authorizer       contracts.Authorizer
	identityProvider contracts.IdentityProvider
	authFailures     map[string]*grpcAuthFailureRecord
	cleanupStop      chan struct{}
	// insecureModuleIDs accepts x-caller-id as the module principal on
	// plaintext connections (dev profile, MUXCORE_INSECURE_DISABLE_TLS).
	insecureModuleIDs bool
	insecureWarnOnce  sync.Once
}

// NewAuthInterceptor creates an auth interceptor with deny-by-default enforcement.
// Only moduleRegistrationMethods are open; all other methods require an
// Authorizer and IdentityProvider to be configured.
func NewAuthInterceptor() *AuthInterceptor {
	a := &AuthInterceptor{
		authFailures:      make(map[string]*grpcAuthFailureRecord),
		cleanupStop:       make(chan struct{}),
		insecureModuleIDs: config.InsecureTLSSkipEnabled(),
	}
	go a.cleanupLoop()
	return a
}

// SetAuthorizer sets the authorizer for permission checks.
func (a *AuthInterceptor) SetAuthorizer(auth contracts.Authorizer) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.authorizer = auth
}

// SetInsecureModuleIdentity controls whether x-caller-id is accepted as the
// module principal on plaintext connections. Defaults to
// config.InsecureTLSSkipEnabled() (MUXCORE_INSECURE_DISABLE_TLS).
func (a *AuthInterceptor) SetInsecureModuleIdentity(enabled bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.insecureModuleIDs = enabled
}

// SetIdentityProvider sets the identity provider for extracting caller identity.
func (a *AuthInterceptor) SetIdentityProvider(ip contracts.IdentityProvider) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.identityProvider = ip
}

// authFailureKey returns the key used for brute-force tracking: the
// authenticated module ID when one was resolved, otherwise the peer address.
// Client-supplied x-caller-id is never used as a key outside the insecure
// profile, so a remote caller cannot lock out another module's identity.
func (a *AuthInterceptor) authFailureKey(ctx context.Context, moduleID string) string {
	if moduleID != "" {
		return moduleID
	}
	if p, ok := peer.FromContext(ctx); ok && p.Addr != nil {
		return p.Addr.String()
	}
	return "unknown"
}

// checkBackoff returns true if the caller is currently in backoff.
// Must be called with a.mu held (read lock is sufficient).
func (a *AuthInterceptor) checkBackoffLocked(key string) bool {
	rec, exists := a.authFailures[key]
	if !exists {
		return false
	}
	return time.Now().Before(rec.blockedUntil)
}

// recordFailure increments the failure count for the given key and applies
// cumulative exponential backoff if the threshold is reached. Must be called
// with a.mu held (write lock). Permanent lockout after excessive total failures.
func (a *AuthInterceptor) recordFailureLocked(key string) {
	now := time.Now()
	rec, exists := a.authFailures[key]
	if !exists {
		rec = &grpcAuthFailureRecord{}
		a.authFailures[key] = rec
	}
	rec.lastActivity = now
	rec.totalFailures++

	// Permanent lockout after excessive total failures.
	if rec.totalFailures >= gRPCAuthDecayLimit {
		rec.blockedUntil = now.Add(100 * 365 * 24 * time.Hour)
	} else if now.After(rec.blockedUntil) {
		rec.count++
		if rec.count >= gRPCAuthBackoffThreshold {
			// Exponential backoff: double each time, capped at gRPCAuthMaxBackoff.
			backoff := gRPCAuthBackoffDuration * (1 << min(rec.totalFailures/gRPCAuthBackoffThreshold, 6))
			if backoff > gRPCAuthMaxBackoff {
				backoff = gRPCAuthMaxBackoff
			}
			rec.blockedUntil = now.Add(backoff)
			rec.count = 0
		}
	}
}

// cleanupLoop periodically purges stale gRPC auth failure records with no
// activity for over 10 minutes.
func (a *AuthInterceptor) cleanupLoop() {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("auth cleanup loop panic recovered", "panic", r)
		}
	}()
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-a.cleanupStop:
			return
		case <-ticker.C:
			a.mu.Lock()
			cutoff := time.Now().Add(-10 * time.Minute)
			for key, rec := range a.authFailures {
				if rec.lastActivity.Before(cutoff) {
					delete(a.authFailures, key)
				}
			}
			a.mu.Unlock()
		}
	}
}

// StopCleanup stops the background cleanup goroutine. Should be called when
// the interceptor is no longer needed.
func (a *AuthInterceptor) StopCleanup() {
	close(a.cleanupStop)
}

// modulePrincipalMethods lists the core gRPC methods a module (service)
// principal may call (ADR-0017 decision 1). Module principals are not checked
// with the user Authorizer; these core rules apply instead, and the services
// enforce the call/publish policy themselves (EventService.Publish via the
// bus publish policy, StorageService and ModuleMesh via the call policy).
// Operator surfaces — lifecycle, spool/marketplace, audit read/verify and
// cluster Leave — require a user principal (bearer token) and are therefore
// absent. Open methods (moduleRegistrationMethods) never reach this check.
var modulePrincipalMethods = map[string]bool{
	"/muxcore.events.v1.EventService/Publish":                 true,
	"/muxcore.events.v1.EventService/Request":                 true,
	"/muxcore.events.v1.EventService/Replay":                  true,
	"/muxcore.events.v1.EventService/Subscribe":               true,
	"/muxcore.storage.v1.StorageService/Put":                  true,
	"/muxcore.storage.v1.StorageService/Get":                  true,
	"/muxcore.storage.v1.StorageService/Delete":               true,
	"/muxcore.storage.v1.StorageService/Stat":                 true,
	"/muxcore.storage.v1.StorageService/List":                 true,
	"/muxcore.storage.v1.StorageService/Capabilities":         true,
	"/muxcore.mesh.v1.ModuleMesh/Call":                        true,
	"/muxcore.mesh.v1.ModuleMesh/StreamCall":                  true,
	"/muxcore.health.v1.HealthService/Check":                  true,
	"/muxcore.health.v1.HealthService/Watch":                  true,
	"/grpc.health.v1.Health/Check":                            true,
	"/grpc.health.v1.Health/Watch":                            true,
	"/muxcore.audit.v1.AuditService/Log":                      true,
	"/muxcore.discovery.v1.DiscoveryService/FindByCapability": true,
	"/muxcore.discovery.v1.DiscoveryService/FindByRole":       true,
	"/muxcore.discovery.v1.DiscoveryService/Resolve":          true,
	"/muxcore.discovery.v1.DiscoveryService/ListAll":          true,
	"/muxcore.discovery.v1.DiscoveryService/Members":          true,
	"/muxcore.discovery.v1.DiscoveryService/Watch":            true,
}

// firstMD returns the first non-empty trimmed value for key in the incoming
// gRPC metadata.
func firstMD(md metadata.MD, key string) string {
	for _, v := range md.Get(key) {
		if v = strings.TrimSpace(v); v != "" {
			return v
		}
	}
	return ""
}

// withoutCallerIDHint returns ctx with x-caller-id removed from the incoming
// metadata so the identity provider never sees a client-supplied module ID
// (ADR-0017: the identity provider is consulted only for user tokens).
func withoutCallerIDHint(ctx context.Context) context.Context {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok || len(md.Get("x-caller-id")) == 0 {
		return ctx
	}
	md = md.Copy()
	md.Delete("x-caller-id")
	return metadata.NewIncomingContext(ctx, md)
}

// resolveModulePrincipal returns the module ID of a caller that carries no
// user bearer token: the CN of a verified client certificate, or — only in
// the insecure/dev profile on a plaintext connection — the x-caller-id
// metadata. Returns "" when the caller is not an authenticated module.
func (a *AuthInterceptor) resolveModulePrincipal(ctx context.Context, fullMethod, claimed string) string {
	if cn, ok := VerifiedModuleID(ctx); ok {
		if claimed != "" && claimed != cn {
			slog.Debug("gRPC x-caller-id ignored: verified certificate identity takes precedence",
				"method", fullMethod, "cert_cn", cn, "x_caller_id", claimed)
		}
		return cn
	}
	a.mu.RLock()
	insecureIDs := a.insecureModuleIDs
	a.mu.RUnlock()
	if insecureIDs && claimed != "" && !peerIsTLS(ctx) {
		a.insecureWarnOnce.Do(func() {
			slog.Warn("gRPC insecure mode: accepting client-supplied x-caller-id as module identity " +
				"(dev profile only; use mTLS module certificates in production)")
		})
		return claimed
	}
	return ""
}

// extractAndVerify performs identity extraction and authorization.
// Returns the context with caller ID set on success, or an error on failure.
//
// Principal resolution (ADR-0017):
//  1. Open methods: caller "_public".
//  2. A user bearer token (authorization metadata): resolved by the identity
//     provider and authorized with Authorizer.Can (unchanged).
//  3. No token, verified client certificate: module principal = cert CN;
//     x-caller-id is ignored. Authorized by modulePrincipalMethods.
//  4. No token, no certificate, insecure/dev profile, plaintext connection:
//     module principal = x-caller-id (warned once). Same rules as 3.
//  5. Otherwise: identity provider without the x-caller-id hint, then Can —
//     a module without a certificate is not authenticated.
func (a *AuthInterceptor) extractAndVerify(ctx context.Context, fullMethod string) (context.Context, error) {
	// ModuleRegistration is always open — modules must register before they
	// can authenticate. Public methods get a default caller ID so downstream
	// service-level auth checks (e.g. discovery.checkAuth) can pass.
	if moduleRegistrationMethods[fullMethod] {
		ctx = callerid.SetPrincipal(ctx, "_public", callerid.KindPublic)
		return ctx, nil
	}

	md, _ := metadata.FromIncomingContext(ctx)

	// Extract trace ID from incoming gRPC metadata for distributed tracing.
	if traceID := firstMD(md, "x-trace-id"); traceID != "" {
		ctx = trace.WithTraceID(ctx, traceID)
	}

	claimedID := firstMD(md, "x-caller-id")
	hasUserToken := firstMD(md, "authorization") != ""

	var moduleID string
	if !hasUserToken {
		moduleID = a.resolveModulePrincipal(ctx, fullMethod, claimedID)
	}

	// Check auth failure backoff before any other processing.
	a.mu.RLock()
	backoffKey := a.authFailureKey(ctx, moduleID)
	inBackoff := a.checkBackoffLocked(backoffKey)
	a.mu.RUnlock()
	if inBackoff {
		slog.Warn("gRPC auth rate limited",
			"method", fullMethod,
			"key", backoffKey,
		)
		return ctx, status.Error(codes.ResourceExhausted, "too many authentication failures")
	}

	if moduleID != "" {
		if !modulePrincipalMethods[fullMethod] {
			// Authenticated module calling an operator method: deny without
			// counting it as an authentication failure.
			slog.Warn("gRPC access denied: method requires a user principal",
				"method", fullMethod,
				"module", moduleID,
			)
			return ctx, status.Error(codes.PermissionDenied, "access denied: method requires a user principal")
		}
		return callerid.SetPrincipal(ctx, moduleID, callerid.KindModule), nil
	}

	a.mu.RLock()
	authorizer := a.authorizer
	identityProvider := a.identityProvider
	a.mu.RUnlock()

	// Authorizer set but no identity provider — misconfiguration.
	if authorizer != nil && identityProvider == nil {
		a.mu.Lock()
		a.recordFailureLocked(backoffKey)
		a.mu.Unlock()
		return ctx, status.Error(codes.Unavailable, "authentication misconfigured: identity provider required when authorizer is set")
	}

	// Full authentication + authorization path.
	if authorizer != nil {
		identity, err := identityProvider.ExtractIdentity(withoutCallerIDHint(ctx))
		if err != nil {
			slog.Warn("gRPC identity extraction failed",
				"method", fullMethod,
				"error", err,
			)
			a.mu.Lock()
			a.recordFailureLocked(backoffKey)
			a.mu.Unlock()
			return ctx, status.Error(codes.Unauthenticated, "identity extraction failed")
		}
		if identity == nil {
			slog.Warn("gRPC call without identity",
				"method", fullMethod,
			)
			a.mu.Lock()
			a.recordFailureLocked(backoffKey)
			a.mu.Unlock()
			return ctx, status.Error(codes.Unauthenticated, "authentication required")
		}

		ctx = callerid.SetPrincipal(ctx, identity.ID, callerid.KindUser)

		session := contracts.Session{
			UserID: identity.ID,
			Roles:  identity.Roles,
		}
		allowed, authErr := authorizer.Can(ctx, session, fullMethod, "*")
		if authErr != nil {
			slog.Error("gRPC authorization check failed",
				"method", fullMethod,
				"identity", identity.ID,
				"error", authErr,
			)
			return ctx, status.Error(codes.Internal, "authorization check failed")
		}
		if !allowed {
			slog.Warn("gRPC access denied",
				"method", fullMethod,
				"identity", identity.ID,
			)
			a.mu.Lock()
			a.recordFailureLocked(backoffKey)
			a.mu.Unlock()
			return ctx, status.Error(codes.PermissionDenied, "access denied")
		}

		return ctx, nil
	}

	slog.Warn("gRPC access denied: no authentication",
		"method", fullMethod,
	)
	a.mu.Lock()
	a.recordFailureLocked(backoffKey)
	a.mu.Unlock()
	return ctx, status.Error(codes.PermissionDenied, "authentication required")
}

// UnaryInterceptor returns a gRPC unary server interceptor.
func (a *AuthInterceptor) UnaryInterceptor() grpc.UnaryServerInterceptor {
	return func(
		ctx context.Context,
		req interface{},
		info *grpc.UnaryServerInfo,
		handler grpc.UnaryHandler,
	) (interface{}, error) {
		ctx, err := a.extractAndVerify(ctx, info.FullMethod)
		if err != nil {
			return nil, err
		}
		return handler(ctx, req)
	}
}

// StreamInterceptor returns a gRPC stream server interceptor.
func (a *AuthInterceptor) StreamInterceptor() grpc.StreamServerInterceptor {
	return func(
		srv interface{},
		stream grpc.ServerStream,
		info *grpc.StreamServerInfo,
		handler grpc.StreamHandler,
	) error {
		ctx, err := a.extractAndVerify(stream.Context(), info.FullMethod)
		if err != nil {
			return err
		}
		wrappedStream := &authServerStream{ServerStream: stream, ctxFn: func() context.Context { return ctx }}
		return handler(srv, wrappedStream)
	}
}

// authServerStream wraps a grpc.ServerStream to override the context with the
// authenticated context after authorization.
type authServerStream struct {
	grpc.ServerStream
	ctxFn func() context.Context
}

func (w *authServerStream) Context() context.Context {
	return w.ctxFn()
}
