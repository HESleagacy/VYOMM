package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"testing"
)

func TestAllMetricsRegisterAndIncrement(t *testing.T) {
	m := New()
	m.TelemetryRecordsReceived.WithLabelValues("trial", "synthetic").Inc()
	m.TelemetryIngestionErrors.WithLabelValues("trial", "validation").Inc()
	m.IncidentsActive.WithLabelValues("trial", "high").Set(1)
	m.IncidentsResolved.WithLabelValues("trial", "high").Inc()
	m.IncidentsRecurred.WithLabelValues("trial").Inc()
	m.ScenarioRuns.WithLabelValues("healthy-baseline", "trial", "pass").Inc()
	m.DetectionLatency.WithLabelValues("trial").Observe(.01)
	m.HTTPDuration.WithLabelValues("GET", "/healthz", "2xx").Observe(.01)
	m.HTTPRequests.WithLabelValues("GET", "/healthz", "2xx").Inc()
	m.HAMiScrapeSuccess.WithLabelValues("trial").Set(1)
	m.SchedulerAllocationEvents.WithLabelValues("nvml-mock", "allocated").Inc()
	m.EvaluationPrecision.Set(.5)
	m.PersistenceOperations.WithLabelValues("restore", "ok").Inc()
	m.PersistencePrunedRows.WithLabelValues("telemetry").Inc()
	m.RecordsDropped.WithLabelValues("trial", "other").Inc()
	// m.Gatherer is statically typed as the narrower prometheus.Gatherer
	// interface (per the Registry struct field), but testutil.CollectAndCount
	// needs a prometheus.Collector. *prometheus.Registry (the concrete type
	// New() actually constructs) implements both interfaces, so the type
	// assertion below is safe and lets the test use the public field as-is
	// rather than requiring metrics.go to expose a second concrete-typed field.
	collector, ok := m.Gatherer.(prometheus.Collector)
	if !ok {
		t.Fatalf("m.Gatherer's concrete type %T does not implement prometheus.Collector", m.Gatherer)
	}
	if n := testutil.CollectAndCount(collector); n != 15 {
		t.Fatalf("registered metric families=%d, want 15", n)
	}
}

func TestLabelsDoNotIncludeDeviceOrRun(t *testing.T) {
	// This test's original assertion (CollectAndCount == 1) reflected a
	// misunderstanding of the API: CollectAndCount on a CounterVec counts
	// time series (one per distinct label combination), not metric
	// families/names, so it can never be 1 once two distinct mode/source
	// combinations are used. The actual requirement from
	// METRICS_CONTRACT.md's cardinality rule is: (a) label values are
	// small, bounded enums (mode/source), not device hostnames or run IDs,
	// so the number of time series stays proportional to the enum size,
	// not the device/request count; and (b) there is still exactly ONE
	// metric name/family registered regardless of how many label
	// combinations are used. Both are asserted below.
	m := New()
	m.TelemetryRecordsReceived.WithLabelValues("trial", "synthetic").Inc()
	m.TelemetryRecordsReceived.WithLabelValues("nvml-mock", "mock").Inc()

	if got := testutil.CollectAndCount(m.TelemetryRecordsReceived); got != 2 {
		t.Fatalf("time series count=%d, want 2 (one per distinct bounded mode/source combination used)", got)
	}

	families, err := m.Gatherer.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	var found int
	for _, f := range families {
		if f.GetName() == "vyomm_telemetry_records_received_total" {
			found++
		}
	}
	if found != 1 {
		t.Fatalf("expected exactly 1 metric family named vyomm_telemetry_records_received_total, found %d", found)
	}
}
