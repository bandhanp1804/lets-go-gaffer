// Package telemetry sends traces and metrics over OTLP (in the cluster, to the Datadog Agent) and
// ties log lines to traces. Without OTEL_EXPORTER_OTLP_ENDPOINT it sends nothing: local runs and
// tests are unchanged, and every helper here is safe to call either way.
package telemetry

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// Enabled reports whether there is somewhere to send telemetry.
func Enabled() bool {
	return strings.TrimSpace(os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")) != ""
}

// Setup installs the global tracer and meter providers for service. The exporters read the
// standard OTEL_* variables (endpoint, headers), and OTEL_SERVICE_NAME and OTEL_RESOURCE_ATTRIBUTES
// (env, version) override what is set here. The returned shutdown flushes what is buffered; call
// it on the way out with a context that still has a few seconds left.
func Setup(ctx context.Context, service string) (shutdown func(context.Context) error, err error) {
	// The W3C header is what Traefik sends and what the Temporal interceptor carries into workflows.
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{}))
	if !Enabled() {
		return func(context.Context) error { return nil }, nil
	}

	// Later options win, so the environment can rename the service.
	res, err := resource.New(ctx,
		resource.WithAttributes(attribute.String("service.name", service)),
		resource.WithFromEnv(),
		resource.WithHost(),
	)
	if err != nil {
		return nil, fmt.Errorf("describe the process for telemetry: %w", err)
	}

	traces, err := otlptracehttp.New(ctx)
	if err != nil {
		return nil, fmt.Errorf("create trace exporter: %w", err)
	}
	tp := sdktrace.NewTracerProvider(sdktrace.WithBatcher(traces), sdktrace.WithResource(res))

	metrics, err := otlpmetrichttp.New(ctx, otlpmetrichttp.WithTemporalitySelector(deltaTemporality))
	if err != nil {
		_ = tp.Shutdown(ctx)
		return nil, fmt.Errorf("create metric exporter: %w", err)
	}
	mp := sdkmetric.NewMeterProvider(
		sdkmetric.WithResource(res),
		sdkmetric.WithReader(sdkmetric.NewPeriodicReader(metrics, sdkmetric.WithInterval(30*time.Second))),
		sdkmetric.WithView(dropLibraryMetrics...),
	)

	otel.SetTracerProvider(tp)
	otel.SetMeterProvider(mp)
	return func(ctx context.Context) error {
		return errors.Join(tp.Shutdown(ctx), mp.Shutdown(ctx))
	}, nil
}

// deltaTemporality sends counters and histograms as the change since the last export, which is
// what Datadog stores; an up-down counter is a level, so it stays cumulative.
func deltaTemporality(k sdkmetric.InstrumentKind) metricdata.Temporality {
	switch k {
	case sdkmetric.InstrumentKindUpDownCounter, sdkmetric.InstrumentKindObservableUpDownCounter:
		return metricdata.CumulativeTemporality
	}
	return metricdata.DeltaTemporality
}

// dropLibraryMetrics turns off the metrics the HTTP instrumentation records on its own. Datadog
// counts every OTLP metric series as a custom metric, and the plan allows only so many; request
// rate, errors and latency come from the traces instead (APM computes them, for free).
var dropLibraryMetrics = []sdkmetric.View{
	sdkmetric.NewView(sdkmetric.Instrument{Name: "http.server.*"}, sdkmetric.Stream{Aggregation: sdkmetric.AggregationDrop{}}),
	sdkmetric.NewView(sdkmetric.Instrument{Name: "http.client.*"}, sdkmetric.Stream{Aggregation: sdkmetric.AggregationDrop{}}),
}
