package mock

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/Muxcore-Media/core/pkg/contracts"
)

// Mesh is a mock of contracts.ModuleMeshClient for testing.
// Calls are recorded in Calls for later inspection. Handlers registered
// via RegisterHandler are invoked for matching target modules.
type Mesh struct {
	mu       sync.RWMutex
	handlers map[string]contracts.MeshHandler
	Calls    []MeshCall
}

// MeshCall records a single Call invocation for test assertions.
type MeshCall struct {
	TargetModule string
	Method       string
	Payload      []byte
}

// NewMesh creates an empty mesh mock.
func NewMesh() *Mesh {
	return &Mesh{
		handlers: make(map[string]contracts.MeshHandler),
	}
}

// Call invokes a registered handler for the target module, or returns
// a not-found error if no handler is registered.
func (m *Mesh) Call(ctx context.Context, targetModule, method string, payload []byte) ([]byte, error) {
	m.mu.Lock()
	m.Calls = append(m.Calls, MeshCall{TargetModule: targetModule, Method: method, Payload: payload})
	m.mu.Unlock()

	m.mu.RLock()
	handler, ok := m.handlers[targetModule]
	m.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("mesh: no handler for module %q", targetModule)
	}
	return handler.HandleCall(ctx, method, payload)
}

// RegisterHandler stores a handler that will receive calls for the given module.
func (m *Mesh) RegisterHandler(moduleID string, handler contracts.MeshHandler) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.handlers[moduleID] = handler
}

// SetHandler is a test helper that registers a simple function-based handler.
func (m *Mesh) SetHandler(moduleID string, fn func(ctx context.Context, method string, payload []byte) ([]byte, error)) {
	m.RegisterHandler(moduleID, &funcHandler{fn: fn})
}

type funcHandler struct {
	fn func(ctx context.Context, method string, payload []byte) ([]byte, error)
}

func (h *funcHandler) HandleCall(ctx context.Context, method string, payload []byte) ([]byte, error) {
	if h.fn == nil {
		return nil, fmt.Errorf("mock: handler function is nil")
	}
	return h.fn(ctx, method, payload)
}

// CallJSON is a test helper that marshals payload as JSON and unmarshals the response.
func (m *Mesh) CallJSON(ctx context.Context, targetModule, method string, payload, response any) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	resp, err := m.Call(ctx, targetModule, method, data)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(resp, response); err != nil {
		return fmt.Errorf("unmarshal: %w", err)
	}
	return nil
}

var _ contracts.ModuleMeshClient = (*Mesh)(nil)
