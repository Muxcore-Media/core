package trace

import (
	"context"
	"testing"

	"github.com/Muxcore-Media/core/internal/config"
)

func TestInitProvider_EmptyConfig(t *testing.T) {
	cfg := config.TraceConfig{Exporter: ""}
	tracer, shutdown, err := InitProvider(cfg)
	if err != nil {
		t.Fatalf("InitProvider with empty config should not error: %v", err)
	}
	if tracer == nil {
		t.Fatal("InitProvider should return non-nil tracer for empty config")
	}
	if shutdown == nil {
		t.Fatal("InitProvider should return non-nil shutdown for empty config")
	}

	// Shutdown should be safe to call with any context.
	if err := shutdown(context.Background()); err != nil {
		t.Errorf("noop shutdown should not error: %v", err)
	}
}

func TestInitProvider_InvalidExporter(t *testing.T) {
	cfg := config.TraceConfig{Exporter: "invalid"}
	_, _, err := InitProvider(cfg)
	if err == nil {
		t.Error("InitProvider with invalid exporter should return an error")
	}
}

func TestInitProvider_OTLPWithoutEndpoint(t *testing.T) {
	cfg := config.TraceConfig{Exporter: "otlp", Endpoint: ""}
	_, _, err := InitProvider(cfg)
	if err == nil {
		t.Error("InitProvider with otlp exporter and no endpoint should return an error")
	}
}
