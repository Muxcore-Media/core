package mock

import (
	"net/http"

	"github.com/Muxcore-Media/core/pkg/contracts"
)

// NewDeps creates a ModuleDeps with mock bus and registry for testing.
func NewDeps() contracts.ModuleDeps {
	bus := NewEventBus()
	reg := NewRegistry()
	return contracts.ModuleDeps{
		Registry: reg,
		EventBus: bus,
		Routes:   &NoopRouteRegistrar{},
	}
}

// NoopRouteRegistrar is a route registrar that discards all registrations.
type NoopRouteRegistrar struct{}

func (n *NoopRouteRegistrar) Handle(pattern string, handler http.Handler)    {}
func (n *NoopRouteRegistrar) HandleFunc(pattern string, handler func(http.ResponseWriter, *http.Request)) {}
