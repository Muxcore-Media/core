package contracts

import (
	"context"
	"time"
)

// TapestryStatus tracks the lifecycle of a running workflow.
type TapestryStatus string

const (
	TapestryPending   TapestryStatus = "pending"
	TapestryRunning   TapestryStatus = "running"
	TapestryCompleted TapestryStatus = "completed"
	TapestryFailed    TapestryStatus = "failed"
	TapestryCancelled TapestryStatus = "cancelled"
	TapestryPaused    TapestryStatus = "paused"
)

// StepResult captures the outcome of one tapestry step.
type StepResult struct {
	StepName   string
	Status     string // "success", "failed", "skipped"
	StartedAt  time.Time
	EndedAt    time.Time
	Attempt    int
	MaxRetries int
	Output     map[string]any // step output data passed to subsequent steps
	Error      string
}

// TapestryRun represents a single execution of a tapestry.
type TapestryRun struct {
	ID           string
	DefinitionID string
	Status       TapestryStatus
	CurrentStep  string
	StepResults  []StepResult
	Input        map[string]any
	StartedAt    time.Time
	EndedAt      time.Time
	Error        string
	Meta         map[string]any
}

// StepHandlerKind identifies how a TapestryStep.Handler is resolved.
type StepHandlerKind string

const (
	// StepHandlerModule routes the step to a specific module by ID.
	// Handler.Ref must be a module ID (e.g. "transcoder-ffmpeg").
	StepHandlerModule StepHandlerKind = "module"

	// StepHandlerCapability routes the step to any module advertising
	// the given capability. Handler.Ref must be a capability string
	// (e.g. "transcoder"). The workflow engine picks an available module.
	StepHandlerCapability StepHandlerKind = "capability"
)

// StepHandler specifies which module handles a tapestry step.
// Exactly one of Kind+Ref must be set.
type StepHandler struct {
	// Kind determines how Ref is interpreted.
	Kind StepHandlerKind
	// Ref is a module ID (Kind=StepHandlerModule) or capability string
	// (Kind=StepHandlerCapability).
	Ref string
}

// TapestryStep defines one step in a tapestry definition.
type TapestryStep struct {
	Name         string
	Handler      StepHandler
	Retry        int // max retry attempts (0 = no retry)
	Timeout      time.Duration
	DependsOn    []string          // step names that must complete before this one
	InputMapping map[string]string // maps step output keys to this step's input keys
	Meta         map[string]any
}

// TapestryDefinition is a registered, reusable workflow template.
type TapestryDefinition struct {
	ID          string
	Name        string
	Description string
	Steps       []TapestryStep
	Version     string
	Meta        map[string]any
}

// WorkflowEngine orchestrates multi-step tapestry executions.
// Discovered via FindByCapability(CapabilityWorkflowEngine). If no module
// implements this contract, individual Scheduler tasks can still run
// independently — tapestry execution is an optional layer.
type WorkflowEngine interface {
	// RegisterDefinition stores a tapestry template for later execution.
	// definition.ID is the key used by Run.
	RegisterDefinition(ctx context.Context, definition TapestryDefinition) error

	// RemoveDefinition removes a previously registered definition.
	RemoveDefinition(ctx context.Context, definitionID string) error

	// GetDefinition returns a registered definition, or nil if not found.
	GetDefinition(ctx context.Context, definitionID string) (*TapestryDefinition, error)

	// ListDefinitions returns all registered definitions.
	ListDefinitions(ctx context.Context) ([]TapestryDefinition, error)

	// Run starts a tapestry execution. input carries the initial
	// data passed to the first step(s).
	Run(ctx context.Context, definitionID string, input map[string]any) (string, error)

	// Status returns the current state of a run.
	Status(ctx context.Context, runID string) (*TapestryRun, error)

	// Cancel stops a running tapestry. Already-completed steps
	// are not rolled back.
	Cancel(ctx context.Context, runID string) error

	// Pause suspends a running tapestry at the next step boundary.
	Pause(ctx context.Context, runID string) error

	// Resume continues a paused tapestry.
	Resume(ctx context.Context, runID string) error

	// ListRuns returns all runs, optionally filtered by status or definitionID.
	ListRuns(ctx context.Context, filter *TapestryRunFilter) ([]TapestryRun, error)
}

// TapestryRunFilter narrows ListRuns queries.
type TapestryRunFilter struct {
	Status       TapestryStatus
	DefinitionID string
}

// Tapestry lifecycle event types — defined here so both core and modules
// can reference them without a separate contract repo for workflow.
const (
	EventTapestryStarted   = "tapestry.started"
	EventTapestryCompleted = "tapestry.completed"
	EventTapestryFailed    = "tapestry.failed"
	EventTapestryCancelled = "tapestry.cancelled"
	EventTapestryPaused    = "tapestry.paused"
	EventTapestryResumed   = "tapestry.resumed"
	EventStepStarted       = "tapestry.step.started"
	EventStepCompleted     = "tapestry.step.completed"
	EventStepFailed        = "tapestry.step.failed"
)

// Tapestry event payloads.
type TapestryStartedPayload struct {
	RunID        string `json:"run_id"`
	DefinitionID string `json:"definition_id"`
}

type TapestryCompletedPayload struct {
	RunID        string `json:"run_id"`
	DefinitionID string `json:"definition_id"`
	Duration     string `json:"duration"`
}

type TapestryFailedPayload struct {
	RunID        string `json:"run_id"`
	DefinitionID string `json:"definition_id"`
	StepName     string `json:"step_name"`
	Error        string `json:"error"`
}

type StepStartedPayload struct {
	RunID    string `json:"run_id"`
	StepName string `json:"step_name"`
	Handler  string `json:"handler"`
	Attempt  int    `json:"attempt"`
}

type StepCompletedPayload struct {
	RunID    string         `json:"run_id"`
	StepName string         `json:"step_name"`
	Duration string         `json:"duration"`
	Output   map[string]any `json:"output"`
}

type StepFailedPayload struct {
	RunID    string `json:"run_id"`
	StepName string `json:"step_name"`
	Attempt  int    `json:"attempt"`
	MaxRetry int    `json:"max_retry"`
	Error    string `json:"error"`
}
