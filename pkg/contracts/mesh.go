package contracts

import "context"

// MeshHandler is implemented by modules that want to receive cross-module gRPC calls.
// The mesh server routes incoming Call(targetModule, method, payload) requests
// to the target module's HandleCall method.
//
// Modules register themselves as handlers during Start():
//
//	deps.Mesh.RegisterHandler("my-module", myHandler)
type MeshHandler interface {
	// HandleCall processes a cross-module method invocation.
	// method is an arbitrary string the caller and handler agree on.
	// payload is the JSON-encoded request body.
	// Returns the JSON-encoded response or an error.
	HandleCall(ctx context.Context, method string, payload []byte) ([]byte, error)
}

// ModuleMeshClient is the interface modules use to call other modules.
// It abstracts whether the target is local (in-process) or remote (gRPC network call).
// Modules receive this via ModuleDeps.Mesh.
type ModuleMeshClient interface {
	// Call invokes a method on the target module. If the target is registered
	// locally, the call is dispatched in-process with zero network overhead.
	// If the target is on a remote node, the call goes over gRPC.
	Call(ctx context.Context, targetModule, method string, payload []byte) ([]byte, error)

	// RegisterHandler registers a local module to receive incoming Call requests.
	// Only call this during module Start(). Modules that don't receive calls
	// don't need to register.
	RegisterHandler(moduleID string, handler MeshHandler)
}
