package contracts

import (
	"context"
	"net/http"
	"sync"
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
	Roles        []string              // module-defined role strings; core imposes no taxonomy
	Description  string
	Author       string
	Capabilities []string              // granular capability strings for routing/discovery
	Contracts    []ContractDeclaration // typed contracts this module implements
	DependsOn    []string              // module IDs that must be initialized before this module starts. Cycles detected by Registry.StartupOrder().
}

type Module interface {
	Info() ModuleInfo
	Init(ctx context.Context) error
	Start(ctx context.Context) error
	Stop(ctx context.Context) error
	Health(ctx context.Context) error
}

// InfrastructureAware is implemented by modules that need late-bound
// infrastructure services (Cluster, WorkerPool, AuditLogger).
type InfrastructureAware interface {
	SetInfrastructure(cluster Cluster, workerPool WorkerPool, audit AuditLogger)
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

// Fabric provides modules with the core fabric services they need during construction.
// Domain-specific services (SecretsProvider, DatabaseProvider, etc.) are discovered
// at runtime via the Registry — they are not pre-wired here.
type Fabric struct {
	Registry   Registry
	EventBus   EventBus
	Routes     RouteRegistrar
	Cluster    Cluster
	Storage    StorageOrchestrator
	WorkerPool WorkerPool
	Audit      AuditLogger
	Mesh       ModuleMeshClient
}

// -- Auto-registration --

var registeredFactories []ModuleFactory
var registeredFactoriesMu sync.Mutex

func Register(factory ModuleFactory) {
	registeredFactoriesMu.Lock()
	registeredFactories = append(registeredFactories, factory)
	registeredFactoriesMu.Unlock()
}

func LoadRegistered(deps Fabric) []Module {
	registeredFactoriesMu.Lock()
	factories := make([]ModuleFactory, len(registeredFactories))
	copy(factories, registeredFactories)
	registeredFactories = nil
	registeredFactoriesMu.Unlock()

	modules := make([]Module, 0, len(factories))
	for _, f := range factories {
		modules = append(modules, f(deps))
	}
	return modules
}
