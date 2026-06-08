package contracts

import (
	"context"
	"net/http"
)

type ModuleKind string

const (
	ModuleKindAuth         ModuleKind = "auth"
	ModuleKindProvider     ModuleKind = "provider"
	ModuleKindDownloader   ModuleKind = "downloader"
	ModuleKindIndexer      ModuleKind = "indexer"
	ModuleKindMediaManager ModuleKind = "media_manager"
	ModuleKindProcessor    ModuleKind = "processor"
	ModuleKindPlayback     ModuleKind = "playback"
	ModuleKindWorkflow     ModuleKind = "workflow"
	ModuleKindStorage      ModuleKind = "storage"
	ModuleKindUI           ModuleKind = "ui"
	ModuleKindAPI          ModuleKind = "api"
	ModuleKindEventBus     ModuleKind = "eventbus"
	ModuleKindScheduler    ModuleKind = "scheduler"
)

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
	Kinds        []ModuleKind
	Description  string
	Author       string
	Capabilities []string
}

// PrimaryKind returns the first kind for display purposes.
// Modules with multiple kinds show their primary (first) kind in UIs.
func (m ModuleInfo) PrimaryKind() string {
	if len(m.Kinds) == 0 {
		return "unknown"
	}
	return string(m.Kinds[0])
}

type Module interface {
	Info() ModuleInfo
	Init(ctx context.Context) error
	Start(ctx context.Context) error
	Stop(ctx context.Context) error
	Health(ctx context.Context) error
}

// ServiceRegistry provides runtime discovery of registered modules.
// Modules receive this to find and communicate with other modules.
type ServiceRegistry interface {
	// FindByKind returns all registered modules of the given kind.
	FindByKind(kind ModuleKind) []ModuleEntry
	// FindByCapability returns all registered modules that declare the given capability.
	FindByCapability(cap string) []ModuleEntry
	// SupportsCapability checks whether a specific module supports the given capability.
	SupportsCapability(moduleID, cap string) bool
	// Resolve returns a single module by ID.
	Resolve(id string) (ModuleEntry, error)
	// ListAll returns every registered module.
	ListAll() []ModuleEntry

	// RegisterMediaSchema registers a metadata schema for a media type.
	// Returns an error if a schema for the same MediaType already exists.
	RegisterMediaSchema(schema MediaTypeSchema) error

	// MediaSchema returns the schema for a given media type, if registered.
	MediaSchema(mediaType MediaType) (MediaTypeSchema, bool)

	// MediaSchemas returns all registered media type schemas.
	MediaSchemas() []MediaTypeSchema
}

// ModuleEntry is a handle to a registered module, providing its info
// and the underlying Module instance for interface type assertion.
type ModuleEntry struct {
	Info   ModuleInfo
	State  ModuleState
	Module Module
}

// RouteRegistrar lets modules register HTTP handlers with the core API server.
type RouteRegistrar interface {
	Handle(pattern string, handler http.Handler)
	HandleFunc(pattern string, handler func(http.ResponseWriter, *http.Request))
}

// ModuleFactory is a constructor for a module. Modules call module.Register
// in their init() to make themselves available for auto-loading.
type ModuleFactory func(deps ModuleDeps) Module

// ModuleDeps provides modules with the core services they need during construction.
type ModuleDeps struct {
	Registry   ServiceRegistry
	EventBus   EventBus
	Routes     RouteRegistrar
	Cluster    Cluster
	Storage    StorageOrchestrator
	WorkerPool WorkerPool
	Audit      AuditLogger
}

// -- Auto-registration --

var registeredFactories []ModuleFactory

// Register is called by module init() functions to make a module available
// for auto-loading. Modules that call this are loaded when LoadRegistered is
// called during bootstrap.
func Register(factory ModuleFactory) {
	registeredFactories = append(registeredFactories, factory)
}

// LoadRegistered creates all registered modules using the provided dependencies.
func LoadRegistered(deps ModuleDeps) []Module {
	modules := make([]Module, 0, len(registeredFactories))
	for _, f := range registeredFactories {
		modules = append(modules, f(deps))
	}
	return modules
}
