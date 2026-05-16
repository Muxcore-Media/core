package trace

import (
	"context"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/exporters/stdout/stdouttrace"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
)

// createOTLPProvider builds an OTLP gRPC exporter and TracerProvider.
func createOTLPProvider(endpoint string) (*sdktrace.TracerProvider, func(context.Context) error, error) {
	exp, err := otlptracegrpc.New(
		context.Background(),
		otlptracegrpc.WithEndpoint(endpoint),
		otlptracegrpc.WithInsecure(),
	)
	if err != nil {
		return nil, nil, err
	}

	res, err := resource.New(context.Background(),
		resource.WithAttributes(semconv.ServiceName("muxcore")),
		resource.WithFromEnv(),
	)
	if err != nil {
		return nil, nil, err
	}

	rate, err := parseSampleRate()
	if err != nil {
		return nil, nil, err
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exp),
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sdktrace.ParentBased(sdktrace.TraceIDRatioBased(rate))),
	)
	otel.SetTracerProvider(tp)

	shutdown := func(ctx context.Context) error {
		ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		if err := tp.Shutdown(ctx); err != nil {
			return err
		}
		return exp.Shutdown(ctx)
	}
	return tp, shutdown, nil
}

// createStdoutProvider builds a stdout exporter TracerProvider for development.
func createStdoutProvider() (*sdktrace.TracerProvider, func(context.Context) error, error) {
	exp, err := stdouttrace.New(stdouttrace.WithPrettyPrint())
	if err != nil {
		return nil, nil, err
	}

	res, err := resource.New(context.Background(),
		resource.WithAttributes(semconv.ServiceName("muxcore")),
	)
	if err != nil {
		return nil, nil, err
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exp),
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
	)
	otel.SetTracerProvider(tp)

	shutdown := func(ctx context.Context) error {
		ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		if err := tp.Shutdown(ctx); err != nil {
			return err
		}
		return exp.Shutdown(ctx)
	}
	return tp, shutdown, nil
}
