package trace

import (
	"context"

	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

const (
	// MetadataTraceID is the gRPC metadata key for distributed trace IDs.
	MetadataTraceID = "x-trace-id"
	// MetadataSpanID is the gRPC metadata key for parent span IDs.
	MetadataSpanID = "x-span-id"
)

// UnaryServerInterceptor extracts trace metadata and starts a server span.
func UnaryServerInterceptor(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (resp interface{}, err error) {
	ctx = ExtractFromIncoming(ctx)
	ctx, span := StartSpan(ctx, info.FullMethod)
	defer func() { span.End(err) }()
	return handler(ctx, req)
}

// UnaryClientInterceptor injects trace metadata into outgoing gRPC calls.
func UnaryClientInterceptor(ctx context.Context, method string, req, reply interface{}, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
	ctx = InjectOutgoing(ctx)
	return invoker(ctx, method, req, reply, cc, opts...)
}

// ExtractFromIncoming reads trace headers from incoming gRPC metadata.
func ExtractFromIncoming(ctx context.Context) context.Context {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return ctx
	}
	traceIDs := md.Get(MetadataTraceID)
	if len(traceIDs) == 0 || traceIDs[0] == "" || !isValidTraceID(traceIDs[0]) {
		return ctx
	}
	ctx = WithTraceID(ctx, traceIDs[0])
	spanIDs := md.Get(MetadataSpanID)
	if len(spanIDs) > 0 && spanIDs[0] != "" {
		ctx = context.WithValue(ctx, spanKey{}, &Span{
			TraceID: traceIDs[0],
			SpanID:  spanIDs[0],
		})
	}
	return ctx
}

// InjectOutgoing adds trace and span IDs to outgoing gRPC metadata.
func InjectOutgoing(ctx context.Context) context.Context {
	traceID := FromContext(ctx)
	if traceID == "" {
		return ctx
	}
	pairs := []string{MetadataTraceID, traceID}
	if span := SpanFromContext(ctx); span != nil && span.SpanID != "" {
		pairs = append(pairs, MetadataSpanID, span.SpanID)
	}
	return metadata.AppendToOutgoingContext(ctx, pairs...)
}
