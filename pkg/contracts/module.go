package contracts

import (
	"context"
	"net/http"
)

// ModuleKind is a user-defined string that categorizes a module's role.
// Core has no opinion about what kinds exist — modules and consumers define them.
// Common conventions (defined in contract repos, not here): "auth", "downloader",
// "indexer", "media_manager", "playback", "storage", "workflow", "transcoder". The WorkflowEngine is discovered via "workflow.engine".
type ModuleKind string

// ContractDeclaration describes a typed contract that a module implements.
// Modules declare these in their Info() so consumers can verify compatibility
// before attempting type assertions. The marketplace uses these to detect
// incompatibilities between installed modules and prospective modules.
type ContractDeclaration struct {
	Repo      string // Go module path of the contract package
	Version   string // semantic version tag (e.g. "v1.0.0")
	Interface string // Go interface name the module implements (e.g. "Downloader")
}

type ModuleState string

const (
	ModuleStateRegistered ModuleState = "registered"
	ModuleStateStarting   ModuleState = "starting"
	ModuleStateRunning    ModuleState = "running"
	ModuleStateDegraded   ModuleState = "degraded"
	ModuleStateStopping   ModuleState = "stopping"
	ModuleStateStopped    ModuleState = "stopped"
)

type ModuleInfo struct {
	ID           string
	Name         string
	Version      string
	Roles        []string // module-defined role strings; core imposes no taxonomy
	Description  string
	Author       string
	Capabilities []string              // granular capability strings for routing/discovery
	Contracts    []ContractDeclaration // typed contracts this module implements
	DependsOn    []string              // module IDs that must be initialized before this module starts. Cycles detected by Registry.StartupOrder().
	// MinCoreVersion is the minimum core version required by this module.
	// Uses SemVer (e.g., "1.2.0"). Empty means compatible with any version.
	// Core rejects modules whose MinCoreVersion is greater than the running
	// core version or targets a different major version.
	MinCoreVersion string
	// HTTPAddr is the address where this module serves HTTP, if any.
	// Example: ":8085" or "0.0.0.0:9200". Leave empty if the module does not
	// expose HTTP. Other modules discover this via ModuleEntry.Info.HTTPAddr
	// after a successful FindByCapability or Resolve call.
	HTTPAddr string
}

type Module interface {
	Info() ModuleInfo
	Init(ctx context.Context) error
	Start(ctx context.Context) error
	Stop(ctx context.Context) error
	Health(ctx context.Context) error
}

// Registry provides runtime discovery of registered modules.
type Registry interface {
	FindByRole(role string) []ModuleEntry
	FindByCapability(cap string) []ModuleEntry
	SupportsCapability(moduleID, cap string) bool
	Resolve(id string) (ModuleEntry, error)
	ListAll() []ModuleEntry

	// StartupOrder returns module IDs in dependency-respecting order.
	// Modules listed in DependsOn appear before the module that depends on them.
	// Returns an error if a dependency cycle is detected.
	StartupOrder() ([]string, error)

	// DependencyGraph returns modules that list the given ID in their DependsOn.
	// Useful for understanding the impact of stopping or removing a module.
	DependencyGraph(id string) ([]string, error)
}

type ModuleEntry struct {
	Info   ModuleInfo
	State  ModuleState
	Module Module
}

type RouteRegistrar interface {
	Handle(pattern string, handler http.Handler)
	HandleFunc(pattern string, handler func(http.ResponseWriter, *http.Request))
}

type ModuleFactory func(deps Fabric) Module

// Fabric provides in-process modules with the core fabric services they need
// during construction. Sidecar modules (the primary model) do not use Fabric —
// they access the same services via the five gRPC services on the mesh address.
//
// Domain-specific services (SecretsProvider, DatabaseProvider, etc.) are
// discovered at runtime via the Registry — they are not pre-wired here.
type Fabric struct {
	Registry   Registry
	EventBus   EventBus
	Routes     RouteRegistrar
	Storage    StorageOrchestrator
	WorkerPool WorkerPool
	Audit      AuditLogger
	Mesh       ModuleMeshClient
}
