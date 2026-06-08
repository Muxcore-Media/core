package module

import "github.com/Muxcore-Media/core/pkg/contracts"

// Register delegates to contracts.Register for module auto-registration.
// Modules call this in their init() to register their factory.
func Register(factory contracts.ModuleFactory) {
	contracts.Register(factory)
}

// LoadRegistered delegates to contracts.LoadRegistered.
func LoadRegistered(deps contracts.ModuleDeps) []contracts.Module {
	return contracts.LoadRegistered(deps)
}
