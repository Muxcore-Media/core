package trace

import (
	"context"
	"fmt"
	"os"
	"strconv"

	"github.com/Muxcore-Media/core/pkg/contracts"
)

// InitProvider initializes the OpenTelemetry tracing provider based on
// environment variables. Returns a contracts.Tracer and a shutdown function
// that flushes pending spans. If no exporter is configured, returns a noop
// tracer — zero telemetry leaves the process.
//
// Environment variables:
//
//	MUXCORE_TRACE_EXPORTER   — "" (noop), "otlp", "stdout"
//	MUXCORE_TRACE_ENDPOINT   — OTLP gRPC endpoint (required when exporter=otlp)
//	MUXCORE_TRACE_SAMPLE_RATE — fraction 0.0–1.0, default 1.0
func InitProvider() (contracts.Tracer, func(context.Context) error, error) {
	exporter := os.Getenv("MUXCORE_TRACE_EXPORTER")
	endpoint := os.Getenv("MUXCORE_TRACE_ENDPOINT")

	switch exporter {
	case "":
		return NewNoopTracer(), func(ctx context.Context) error { return nil }, nil

	case "otlp":
		if endpoint == "" {
			return nil, nil, fmt.Errorf("MUXCORE_TRACE_ENDPOINT is required when MUXCORE_TRACE_EXPORTER=otlp")
		}
		return newOTLPProvider(endpoint)

	case "stdout":
		return newStdoutProvider()

	default:
		return nil, nil, fmt.Errorf("unknown trace exporter: %q (valid: otlp, stdout)", exporter)
	}
}

// newOTLPProvider creates an OTLP gRPC exporter backed TracerProvider.
func newOTLPProvider(endpoint string) (contracts.Tracer, func(context.Context) error, error) {
	tp, shutdown, err := createOTLPProvider(endpoint)
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

// parseSampleRate reads MUXCORE_TRACE_SAMPLE_RATE with validation.
func parseSampleRate() (float64, error) {
	v := os.Getenv("MUXCORE_TRACE_SAMPLE_RATE")
	if v == "" {
		return 1.0, nil
	}
	rate, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return 0, fmt.Errorf("MUXCORE_TRACE_SAMPLE_RATE must be a float between 0.0 and 1.0, got %q", v)
	}
	if rate < 0.0 || rate > 1.0 {
		return 0, fmt.Errorf("MUXCORE_TRACE_SAMPLE_RATE must be between 0.0 and 1.0, got %f", rate)
	}
	return rate, nil
}
