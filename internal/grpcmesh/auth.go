package grpcmesh

import (
	"context"
	"log/slog"
	"sync"

	"github.com/Muxcore-Media/core/pkg/contracts"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// AuthInterceptor provides a gRPC unary interceptor that extracts caller
// identity from gRPC metadata and, when an Authorizer is configured, enforces
// per-method access control.
//
// Behavior:
//   - Extracts x-caller-id from metadata and propagates it into the context.
//   - When both IdentityProvider and Authorizer are set: extracts identity,
//     checks authorization via Authorizer.Can(), and denies with
//     PermissionDenied if not authorized.
//   - When neither is set: logs debug-level warnings for anonymous calls
//     but allows all traffic (backward compatible with dev/testing).
type AuthInterceptor struct {
	mu               sync.RWMutex
	authorizer       contracts.Authorizer
	identityProvider contracts.IdentityProvider
}

// NewAuthInterceptor creates an auth interceptor with no enforcement.
// Call SetAuthorizer and SetIdentityProvider to enable enforcement.
func NewAuthInterceptor() *AuthInterceptor {
	return &AuthInterceptor{}
}

// SetAuthorizer sets the authorizer for permission checks.
// Pass nil to disable enforcement (dev/testing mode).
func (a *AuthInterceptor) SetAuthorizer(auth contracts.Authorizer) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.authorizer = auth
}

// SetIdentityProvider sets the identity provider for extracting caller identity.
func (a *AuthInterceptor) SetIdentityProvider(ip contracts.IdentityProvider) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.identityProvider = ip
}

// UnaryInterceptor returns a gRPC unary server interceptor.
func (a *AuthInterceptor) UnaryInterceptor() grpc.UnaryServerInterceptor {
	return func(
		ctx context.Context,
		req interface{},
		info *grpc.UnaryServerInfo,
		handler grpc.UnaryHandler,
	) (interface{}, error) {
		// Extract caller identity from gRPC metadata.
		if md, ok := metadata.FromIncomingContext(ctx); ok {
			if vals := md.Get("x-caller-id"); len(vals) > 0 {
				ctx = contracts.WithCallerID(ctx, vals[0])
			}
		}

		a.mu.RLock()
		authorizer := a.authorizer
		identityProvider := a.identityProvider
		a.mu.RUnlock()

		// If neither is configured, log and allow (backward compat).
		if authorizer == nil || identityProvider == nil {
			if callerID := contracts.CallerIDFromContext(ctx); callerID == "" {
				slog.Debug("gRPC call without caller identity (no authorizer configured)",
					"method", info.FullMethod,
				)
			}
			return handler(ctx, req)
		}

		// Enforcement path: extract identity, check authorization.
		identity, err := identityProvider.ExtractIdentity(ctx)
		if err != nil {
			slog.Warn("gRPC identity extraction failed",
				"method", info.FullMethod,
				"error", err,
			)
			return nil, status.Error(codes.Unauthenticated, "identity extraction failed")
		}
		if identity == nil {
			slog.Warn("gRPC call without identity",
				"method", info.FullMethod,
			)
			return nil, status.Error(codes.Unauthenticated, "authentication required")
		}

		// Build a session from the identity for the authorizer.
		session := contracts.Session{
			UserID: identity.ID,
			Roles:  identity.Roles,
		}
		allowed, authErr := authorizer.Can(ctx, session, info.FullMethod, "*")
		if authErr != nil {
			slog.Error("gRPC authorization check failed",
				"method", info.FullMethod,
				"identity", identity.ID,
				"error", authErr,
			)
			return nil, status.Error(codes.Internal, "authorization check failed")
		}
		if !allowed {
			slog.Warn("gRPC access denied",
				"method", info.FullMethod,
				"identity", identity.ID,
			)
			return nil, status.Error(codes.PermissionDenied, "access denied")
		}

		return handler(ctx, req)
	}
}

// AuthUnaryInterceptor returns a gRPC unary interceptor (deprecated shim).
// Use NewAuthInterceptor().UnaryInterceptor() for new code.
func AuthUnaryInterceptor() grpc.UnaryServerInterceptor {
	slog.Warn("AuthUnaryInterceptor() is deprecated: use NewAuthInterceptor() for configurable enforcement",
		"note", "AuthUnaryInterceptor does not enforce authorization — it only propagates caller identity",
	)
	return NewAuthInterceptor().UnaryInterceptor()
}
