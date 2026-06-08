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

// AuthInterceptor provides gRPC unary and stream interceptors that extract
// caller identity from gRPC metadata and, when an Authorizer and
// IdentityProvider are both configured, enforce per-method access control.
//
// Behavior:
//   - Extracts x-caller-id from metadata and propagates it into the context
//     only AFTER successful identity verification.
//   - When both IdentityProvider and Authorizer are set: extracts identity,
//     checks authorization via Authorizer.Can(), and denies with
//     PermissionDenied if not authorized.
//   - When EITHER is nil: logs a warning and denies all requests (fail-closed)
//     rather than silently allowing all traffic.
type AuthInterceptor struct {
	mu               sync.RWMutex
	authorizer       contracts.Authorizer
	identityProvider contracts.IdentityProvider
}

// NewAuthInterceptor creates an auth interceptor with no enforcement.
// Call SetAuthorizer and SetIdentityProvider to enable enforcement.
// Until both are set, all requests are denied.
func NewAuthInterceptor() *AuthInterceptor {
	return &AuthInterceptor{}
}

// SetAuthorizer sets the authorizer for permission checks.
// Pass nil to indicate no authorizer is available; requests will be denied.
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

// isReady returns true only when both authorizer and identityProvider are set.
func (a *AuthInterceptor) isReady() bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.authorizer != nil && a.identityProvider != nil
}

// extractAndVerify performs identity extraction and authorization.
// Returns the context with caller ID set on success, or an error on failure.
func (a *AuthInterceptor) extractAndVerify(ctx context.Context, fullMethod string) (context.Context, error) {
	a.mu.RLock()
	authorizer := a.authorizer
	identityProvider := a.identityProvider
	a.mu.RUnlock()

	if authorizer == nil || identityProvider == nil {
		return ctx, status.Error(codes.Unavailable, "authentication not configured")
	}

	identity, err := identityProvider.ExtractIdentity(ctx)
	if err != nil {
		slog.Warn("gRPC identity extraction failed",
			"method", fullMethod,
			"error", err,
		)
		return ctx, status.Error(codes.Unauthenticated, "identity extraction failed")
	}
	if identity == nil {
		slog.Warn("gRPC call without identity",
			"method", fullMethod,
		)
		return ctx, status.Error(codes.Unauthenticated, "authentication required")
	}

	// Only propagate caller ID after successful identity verification.
	ctx = contracts.WithCallerID(ctx, identity.ID)

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
		return ctx, status.Error(codes.PermissionDenied, "access denied")
	}

	return ctx, nil
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
		wrappedStream := &authServerStream{ServerStream: stream, ctx: ctx}
		return handler(srv, wrappedStream)
	}
}

// authServerStream wraps a grpc.ServerStream to override the context with the
// authenticated context after authorization.
type authServerStream struct {
	grpc.ServerStream
	ctx context.Context
}

func (w *authServerStream) Context() context.Context {
	return w.ctx
}

// AuthUnaryInterceptor returns a gRPC unary interceptor (deprecated shim).
// Use NewAuthInterceptor().UnaryInterceptor() for new code.
func AuthUnaryInterceptor() grpc.UnaryServerInterceptor {
	slog.Warn("AuthUnaryInterceptor() is deprecated: use NewAuthInterceptor() for configurable enforcement",
		"note", "AuthUnaryInterceptor does not enforce authorization — it only propagates caller identity",
	)
	return NewAuthInterceptor().UnaryInterceptor()
}
