package tracing

import (
	"context"
	"testing"

	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func TestSpanConstants(t *testing.T) {
	if SpanScenarioStarted != "scenario.started" || SpanDecisionRecorded != "decision.recorded" {
		t.Fatal("workflow span constants changed")
	}
}

func TestInMemorySpanRecords(t *testing.T) {
	// Note: span count must be checked BEFORE provider.Shutdown(), not
	// after. tracetest.InMemoryExporter.Shutdown() calls Reset() on the
	// exporter internally, clearing recorded spans — checking afterward
	// always sees zero regardless of whether spans were actually recorded.
	// (Confirmed by direct experiment: with WithSyncer, span.End() exports
	// synchronously, so the span is already present immediately after End.)
	exporter := tracetest.NewInMemoryExporter()
	provider := newTestProvider(exporter)
	tracer := provider.Tracer("test")
	_, span := tracer.Start(context.Background(), string(SpanTelemetryGenerated))
	span.End()

	if got := len(exporter.GetSpans()); got != 1 {
		t.Fatalf("spans=%d, want 1", got)
	}

	if err := provider.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func newTestProvider(exporter *tracetest.InMemoryExporter) *sdktrace.TracerProvider {
	res := resource.Empty()
	return sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter), sdktrace.WithResource(res))
}
