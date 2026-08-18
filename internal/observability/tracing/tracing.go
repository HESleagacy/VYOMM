// Package tracing configures the OTel SDK used by VYOMM services.
package tracing

import (
	"context"
	"net/url"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/semconv/v1.27.0"
	"go.opentelemetry.io/otel/trace"
)

type SpanName string

const (
	SpanScenarioStarted    SpanName = "scenario.started"
	SpanTelemetryGenerated SpanName = "telemetry.generated"
	SpanTelemetryIngested  SpanName = "telemetry.ingested"
	SpanAnomalyDetected    SpanName = "anomaly.detected"
	SpanIncidentCorrelated SpanName = "incident.correlated"
	SpanRunbookRetrieved   SpanName = "runbook.retrieved"
	SpanDecisionRecorded   SpanName = "decision.recorded"
)

func Init(ctx context.Context, endpoint string, sampleRatio float64, serviceName string) (func(context.Context) error, error) {
	u, err := url.Parse(endpoint)
	if err != nil {
		return nil, err
	}
	exporter, err := otlptracehttp.New(ctx, otlptracehttp.WithEndpoint(u.Host), otlptracehttp.WithURLPath(u.Path), otlptracehttp.WithInsecure())
	if err != nil {
		return nil, err
	}
	res, err := resource.New(ctx, resource.WithAttributes(semconv.ServiceName(serviceName)))
	if err != nil {
		return nil, err
	}
	provider := sdktrace.NewTracerProvider(sdktrace.WithBatcher(exporter), sdktrace.WithResource(res), sdktrace.WithSampler(sdktrace.ParentBased(sdktrace.TraceIDRatioBased(sampleRatio))))
	otel.SetTracerProvider(provider)
	return provider.Shutdown, nil
}

func Tracer(name string) trace.Tracer { return otel.Tracer(name) }
