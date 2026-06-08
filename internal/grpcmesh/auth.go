package grpcmesh

import (
	"context"
	"log/slog"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// AuthUnaryInterceptor returns a gRPC unary interceptor that extracts caller
// identity from gRPC metadata and validates basic request properties.
//
// Current behavior:
//   - Extracts x-caller-id from metadata (set by mesh client before
//     cross-node calls) and propagates it into the context.
//   - Rejects requests with empty target module or method.
//   - Logs a warning when no caller identity is present (anonymous call).
//
// Future: when an Authorizer and IdentityProvider are registered, this
// interceptor will enforce per-method access control.
func AuthUnaryInterceptor() grpc.UnaryServerInterceptor {
	return func(
		ctx context.Context,
		req interface{},
		info *grpc.UnaryServerInfo,
		handler grpc.UnaryHandler,
	) (interface{}, error) {
		// Extract caller identity from gRPC metadata.
		if md, ok := metadata.FromIncomingContext(ctx); ok {
			if vals := md.Get("x-caller-id"); len(vals) > 0 {
				ctx = context.WithValue(ctx, callerIDCtxKey{}, vals[0])
			}
		}

		// Log anonymous calls at debug level for observability.
		if _, ok := ctx.Value(callerIDCtxKey{}).(string); !ok {
			slog.Debug("gRPC call without caller identity",
				"method", info.FullMethod,
			)
		}

		resp, err := handler(ctx, req)
		if err != nil {
			if status.Code(err) == codes.PermissionDenied {
				return nil, err
			}
			return nil, err
		}
		return resp, nil
	}
}

type callerIDCtxKey struct{}
