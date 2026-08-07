package module

import "google.golang.org/grpc"

// NewGRPCServer creates a module-facing gRPC server.
// The plaintext flag is retained for API compatibility with modules that pass
// their TLS/dev insecure setting; credentials are configured by the caller or
// via future SDK options.
func NewGRPCServer(plaintext bool) (*grpc.Server, error) {
	_ = plaintext
	return grpc.NewServer(), nil
}
