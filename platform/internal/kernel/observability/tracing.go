package observability

import (
	"context"
	"fmt"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"

	"go.klarlabs.de/glossa/platform/internal/kernel/config"
)

// ShutdownFunc flushes and stops a telemetry provider.
type ShutdownFunc func(context.Context) error

// NewTracerProvider returns an OTLP/HTTP-exporting tracer provider when
// an endpoint is configured and a no-op one otherwise. The exporter and
// sampler honour the standard OTEL_* variables (headers, protocol,
// OTEL_TRACES_SAMPLER …).
func NewTracerProvider(ctx context.Context, cfg config.OTel, version string) (trace.TracerProvider, ShutdownFunc, error) {
	if !cfg.Enabled() {
		return noop.NewTracerProvider(), func(context.Context) error { return nil }, nil
	}
	exporter, err := otlptracehttp.New(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("observability: otlp exporter: %w", err)
	}
	res, err := resource.Merge(resource.Default(), resource.NewSchemaless(
		attribute.String("service.name", cfg.ServiceName),
		attribute.String("service.version", version),
	))
	if err != nil {
		return nil, nil, fmt.Errorf("observability: resource: %w", err)
	}
	tp := sdktrace.NewTracerProvider(sdktrace.WithBatcher(exporter), sdktrace.WithResource(res))
	return tp, tp.Shutdown, nil
}

// NewPropagator returns the W3C trace-context and baggage propagator.
func NewPropagator() propagation.TextMapPropagator {
	return propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{})
}
