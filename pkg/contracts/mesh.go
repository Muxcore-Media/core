package contracts

import "context"

// ModuleMeshClient enables modules to call other modules across nodes.
// The implementation is provided by a cluster module.
// Modules receive this in ModuleDeps and never import gRPC directly.
//
// Call sends a request to a module on a remote or local node.
// payload is JSON-encoded request data.
// Returns JSON-encoded response bytes and optional response headers.
// Errors may be transport errors (node unreachable) or application errors
// from the target module.
type ModuleMeshClient interface {
	Call(ctx context.Context, nodeID, moduleID, method string,
		payload []byte, headers map[string]string) ([]byte, map[string]string, error)
}
