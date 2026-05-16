package trace

import (
	"context"
	"fmt"

	"github.com/Muxcore-Media/core/internal/config"
	"github.com/Muxcore-Media/core/pkg/contracts"
)

// InitProvider initializes the OpenTelemetry tracing provider from a TraceConfig.
// Returns a contracts.Tracer and a shutdown function that flushes pending spans.
// If no exporter is configured, returns a noop tracer — zero telemetry leaves
// the process.
func InitProvider(cfg config.TraceConfig) (contracts.Tracer, func(context.Context) error, error) {
	switch cfg.Exporter {
	case "":
		return NewNoopTracer(), func(ctx context.Context) error { return nil }, nil

	case "otlp":
		if cfg.Endpoint == "" {
			return nil, nil, fmt.Errorf("trace.endpoint is required when trace.exporter is otlp")
		}
		return newOTLPProvider(cfg.Endpoint, cfg.SampleRate)

	case "stdout":
		return newStdoutProvider()

	default:
		return nil, nil, fmt.Errorf("unknown trace exporter: %q (valid: otlp, stdout)", cfg.Exporter)
	}
}

// newOTLPProvider creates an OTLP gRPC exporter backed TracerProvider.
func newOTLPProvider(endpoint string, sampleRate float64) (contracts.Tracer, func(context.Context) error, error) {
	tp, shutdown, err := createOTLPProvider(endpoint, sampleRate)
	if err != nil {
		return nil, nil, fmt.Errorf("otlp trace provider: %w", err)
	}

	tracer := newOTELLibTracer(tp.Tracer("muxcore"), nil)

	return tracer, shutdown, nil
}

// newStdoutProvider creates a stdout exporter for development.
func newStdoutProvider() (contracts.Tracer, func(context.Context) error, error) {
	tp, shutdown, err := createStdoutProvider()
	if err != nil {
		return nil, nil, fmt.Errorf("stdout trace provider: %w", err)
	}

	tracer := newOTELLibTracer(tp.Tracer("muxcore"), nil)

	return tracer, shutdown, nil
}
