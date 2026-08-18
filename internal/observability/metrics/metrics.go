// Package metrics owns VYOMM's bounded-cardinality Prometheus instruments.
// The Controller mounts Handler() at the API's /metrics route.
package metrics

import (
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

type Registry struct {
	Registerer                prometheus.Registerer
	Gatherer                  prometheus.Gatherer
	TelemetryRecordsReceived  *prometheus.CounterVec
	TelemetryIngestionErrors  *prometheus.CounterVec
	IncidentsActive           *prometheus.GaugeVec
	IncidentsResolved         *prometheus.CounterVec
	IncidentsRecurred         *prometheus.CounterVec
	ScenarioRuns              *prometheus.CounterVec
	DetectionLatency          prometheus.ObserverVec
	HTTPDuration              prometheus.ObserverVec
	HTTPRequests              *prometheus.CounterVec
	HAMiScrapeSuccess         *prometheus.GaugeVec
	SchedulerAllocationEvents *prometheus.CounterVec
	EvaluationPrecision       prometheus.Gauge
	PersistenceOperations     *prometheus.CounterVec
	PersistencePrunedRows     *prometheus.CounterVec
	RecordsDropped            *prometheus.CounterVec
}

func New() *Registry {
	r := prometheus.NewRegistry()
	m := &Registry{Registerer: r, Gatherer: r}
	m.TelemetryRecordsReceived = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "vyomm_telemetry_records_received_total"}, []string{"mode", "source"})
	m.TelemetryIngestionErrors = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "vyomm_telemetry_ingestion_errors_total"}, []string{"mode", "reason_class"})
	m.IncidentsActive = prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "vyomm_incidents_active"}, []string{"mode", "severity"})
	m.IncidentsResolved = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "vyomm_incidents_resolved_total"}, []string{"mode", "severity"})
	m.IncidentsRecurred = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "vyomm_incidents_recurred_total"}, []string{"mode"})
	m.ScenarioRuns = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "vyomm_scenario_runs_total"}, []string{"scenario_name", "mode", "result"})
	m.DetectionLatency = prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: "vyomm_detection_latency_seconds"}, []string{"mode"})
	m.HTTPDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: "vyomm_http_request_duration_seconds"}, []string{"method", "route", "status_class"})
	m.HTTPRequests = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "vyomm_http_requests_total"}, []string{"method", "route", "status_class"})
	m.HAMiScrapeSuccess = prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "vyomm_hami_scrape_success"}, []string{"mode"})
	m.SchedulerAllocationEvents = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "vyomm_scheduler_allocation_events_total"}, []string{"mode", "event_type"})
	m.EvaluationPrecision = prometheus.NewGauge(prometheus.GaugeOpts{Name: "vyomm_evaluation_precision"})
	m.PersistenceOperations = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "vyomm_persistence_operations_total"}, []string{"operation", "result"})
	m.PersistencePrunedRows = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "vyomm_persistence_pruned_rows_total"}, []string{"table"})
	m.RecordsDropped = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "vyomm_records_dropped_total"}, []string{"mode", "reason_class"})
	for _, collector := range []prometheus.Collector{m.TelemetryRecordsReceived, m.TelemetryIngestionErrors, m.IncidentsActive, m.IncidentsResolved, m.IncidentsRecurred, m.ScenarioRuns, m.DetectionLatency, m.HTTPDuration, m.HTTPRequests, m.HAMiScrapeSuccess, m.SchedulerAllocationEvents, m.EvaluationPrecision, m.PersistenceOperations, m.PersistencePrunedRows, m.RecordsDropped} {
		r.MustRegister(collector)
	}
	return m
}

func (m *Registry) Handler() http.Handler {
	return promhttp.HandlerFor(m.Gatherer, promhttp.HandlerOpts{})
}
