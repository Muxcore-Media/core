package contracts

import "context"

type WorkflowStep struct {
	Name    string
	Handler string
	Input   map[string]any
	Retry   int
	Timeout int
}

type WorkflowDefinition struct {
	ID    string
	Name  string
	Steps []WorkflowStep
}

type WorkflowRun struct {
	ID         string
	WorkflowID string
	Status     string
	CurrentStep int
	Steps      []StepResult
}

type StepResult struct {
	Name   string
	Status string
	Output map[string]any
	Error  string
}

type WorkflowEngine interface {
	Define(ctx context.Context, def WorkflowDefinition) error
	Run(ctx context.Context, workflowID string, params map[string]any) (string, error)
	Status(ctx context.Context, runID string) (WorkflowRun, error)
	Cancel(ctx context.Context, runID string) error
}
