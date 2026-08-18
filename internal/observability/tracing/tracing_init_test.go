package tracing

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// TestInit_ActuallyExportsOverHTTP verifies the real wire-level behavior of
// Init: a fake OTLP collector (plain httptest.Server, not a full
// Jaeger/Docker instance — Docker was unavailable in the environment this
// was verified in) receives an actual POST request when a span is created
// and the tracer provider is shut down (flushing the batch). This proves
// tracing.Init wires up a working exporter, not just that the SDK objects
// construct without error.
func TestInit_ActuallyExportsOverHTTP(t *testing.T) {
	var received atomic.Int32
	var gotContentType string
	fakeCollector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/traces" {
			received.Add(1)
			gotContentType = r.Header.Get("Content-Type")
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer fakeCollector.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	shutdown, err := Init(ctx, fakeCollector.URL, 1.0, "vyomm-test")
	if err != nil {
		t.Fatalf("unexpected error from Init: %v", err)
	}

	_, span := Tracer("test").Start(ctx, string(SpanTelemetryIngested))
	span.End()

	if err := shutdown(ctx); err != nil {
		t.Fatalf("unexpected error from shutdown: %v", err)
	}

	if received.Load() == 0 {
		t.Fatal("expected the fake OTLP collector to receive at least one POST to /v1/traces, got none")
	}
	if gotContentType == "" {
		t.Error("expected a Content-Type header on the exported request")
	}
}
