package contracts

import "context"

type ModuleKind string

const (
	ModuleKindAuth         ModuleKind = "auth"
	ModuleKindProvider     ModuleKind = "provider"
	ModuleKindDownloader   ModuleKind = "downloader"
	ModuleKindMediaManager ModuleKind = "media_manager"
	ModuleKindProcessor    ModuleKind = "processor"
	ModuleKindPlayback     ModuleKind = "playback"
	ModuleKindWorkflow     ModuleKind = "workflow"
	ModuleKindStorage      ModuleKind = "storage"
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
	ID          string
	Name        string
	Version     string
	Kind        ModuleKind
	Description string
	Author      string
	Capabilities []string
}

type Module interface {
	Info() ModuleInfo
	Init(ctx context.Context) error
	Start(ctx context.Context) error
	Stop(ctx context.Context) error
	Health(ctx context.Context) error
}
