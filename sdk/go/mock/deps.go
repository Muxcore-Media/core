package mock

import (
	"net/http"
	"sync"

	"github.com/Muxcore-Media/core/pkg/contracts"
)

// NewDeps creates a Fabric with mock bus and registry for testing.
func NewDeps() contracts.Fabric {
	bus := NewEventBus()
	reg := NewRegistry()
	return contracts.Fabric{
		Registry: reg,
		EventBus: bus,
		Routes:   &NoopRouteRegistrar{},
	}
}

// NoopRouteRegistrar is a route registrar that discards all registrations
// but tracks them for test introspection via RegisteredRoutes().
type NoopRouteRegistrar struct {
	mu     sync.Mutex
	routes []string
}

func (n *NoopRouteRegistrar) Handle(pattern string, handler http.Handler) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.routes = append(n.routes, pattern)
}

func (n *NoopRouteRegistrar) HandleFunc(pattern string, handler func(http.ResponseWriter, *http.Request)) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.routes = append(n.routes, pattern)
}

// RegisteredRoutes returns the patterns registered via Handle/HandleFunc since
// creation. Useful for test introspection to verify modules registered expected routes.
func (n *NoopRouteRegistrar) RegisteredRoutes() []string {
	n.mu.Lock()
	defer n.mu.Unlock()
	out := make([]string, len(n.routes))
	copy(out, n.routes)
	return out
}
