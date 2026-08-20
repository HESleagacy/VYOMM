package api

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"

	"github.com/GrandRegentSarva/VYOMM/internal/detection"
	"github.com/GrandRegentSarva/VYOMM/internal/forecast"
	"github.com/GrandRegentSarva/VYOMM/internal/incidents"
	"github.com/GrandRegentSarva/VYOMM/internal/observability/tracing"
	"github.com/GrandRegentSarva/VYOMM/internal/telemetry"
)

const timeRFC3339 = time.RFC3339

// tracer is the package-wide tracer for the "vyomm-api" bounded workflow
// spans. Safe to call even if tracing.Init was never invoked (e.g. in unit
// tests): otel's global Tracer() delegates to a no-op provider until a real
// one is set via SetTracerProvider, so handlers behave identically with or
// without a configured exporter.
var tracer = tracing.Tracer("vyomm-api")

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	otelStatus := s.OTelStatus
	if otelStatus == "" {
		otelStatus = "disabled"
	}

	// Real database liveness check, not a static "ok": API_CONTRACT.md
	// requires /healthz to report actual dependency status. A short timeout
	// keeps the probe responsive even when the single serialized SQLite
	// connection is momentarily busy.
	dbStatus := "ok"
	if s.Store == nil {
		dbStatus = "unavailable"
	} else {
		pingCtx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		if err := s.Store.Ping(pingCtx); err != nil {
			dbStatus = "error"
			if s.Logger != nil {
				s.Logger.Error("healthz database check failed", "event", "health.database.failed", "error", err)
			}
		}
		cancel()
	}

	// Reflect the real LLM configuration rather than a hardcoded string.
	// The Go API does not call the provider itself yet, so a configured key
	// reports "configured" and an empty key reports "fallback" (the
	// deterministic offline analysis) — never claimed as "live".
	llmStatus := "fallback"
	if s.Config.HasRealLLMKey() {
		llmStatus = "configured"
	}

	status := "ok"
	code := http.StatusOK
	if dbStatus != "ok" {
		status = "degraded"
		code = http.StatusServiceUnavailable
	}

	writeJSON(w, code, healthDTO{
		Status: status,
		Mode:   string(s.Config.EnvironmentMode),
		Checks: map[string]string{
			"database":      dbStatus,
			"llm_provider":  llmStatus,
			"otel_exporter": otelStatus,
		},
		Version: s.Version,
		RunID:   s.RunID,
	})
}

// handleIngestTelemetry implements the bounded workflow's middle three
// steps as one parent span with two child spans: telemetry.ingested
// (this span) -> anomaly.detected -> incident.correlated. The two earlier
// steps (scenario.started, telemetry.generated) happen in the simulator
// process, which does not yet propagate trace context over HTTP (tracked
// as a follow-up in docs/migration-plan.md); this handler always starts a
// fresh root span rather than silently failing to continue a trace that
// was never sent.
func (s *Server) handleIngestTelemetry(w http.ResponseWriter, r *http.Request) {
	ctx, span := tracer.Start(r.Context(), string(tracing.SpanTelemetryIngested))
	defer span.End()

	var req telemetryIngestRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		span.SetStatus(codes.Error, "malformed request body")
		writeError(w, http.StatusBadRequest, "invalid_request", "malformed JSON body: "+err.Error())
		return
	}
	span.SetAttributes(
		attribute.String("vyomm.run_id", req.RunID),
		attribute.String("vyomm.scenario_id", req.ScenarioID),
		attribute.Int("vyomm.device_count", len(req.Devices)),
	)

	now := s.Clock.Now()
	result, err := s.Store.Ingest(req.Devices, req.RunID, now)
	if err != nil {
		span.SetStatus(codes.Error, err.Error())
		s.Logger.Error("telemetry ingest failed", "event", "telemetry.ingest.failed", "error", err)
		writeError(w, http.StatusInternalServerError, "ingest_failed", "failed to persist telemetry batch")
		return
	}
	span.SetAttributes(
		attribute.Int("vyomm.accepted", result.Accepted),
		attribute.Int("vyomm.rejected", result.Rejected),
	)
	if s.Metrics != nil {
		mode := string(s.Config.EnvironmentMode)
		if result.Accepted > 0 {
			s.Metrics.TelemetryRecordsReceived.WithLabelValues(mode, "synthetic").Add(float64(result.Accepted))
		}
		if result.Rejected > 0 {
			s.Metrics.TelemetryIngestionErrors.WithLabelValues(mode, "validation").Add(float64(result.Rejected))
		}
	}

	s.detectAnomalies(ctx, result.ValidDevices, now)
	s.correlateIncidents(ctx, result.ValidDevices, now)
	writeJSON(w, http.StatusOK, telemetryIngestResponse{Accepted: result.Accepted, Rejected: result.Rejected, Errors: result.Errors})
}

// detectAnomalies is the "anomaly.detected" step of the bounded workflow, a
// child span of the ingest span carried in ctx.
func (s *Server) detectAnomalies(ctx context.Context, devices []telemetry.DeviceTelemetry, now time.Time) {
	_, span := tracer.Start(ctx, string(tracing.SpanAnomalyDetected))
	defer span.End()
	anomalies := detection.Detect(devices, now)
	s.Store.RecordAnomalies(anomalies)
	span.SetAttributes(attribute.Int("vyomm.anomalies_detected", len(anomalies)))
}

// correlateIncidents runs the incident correlation rules against the
// already-validated batch and upserts any matches, matching the original
// behavior of correlating on every ingest rather than on a separate poll
// loop. This is the "incident.correlated" step of the bounded workflow.
func (s *Server) correlateIncidents(ctx context.Context, valid []telemetry.DeviceTelemetry, now time.Time) {
	_, span := tracer.Start(ctx, string(tracing.SpanIncidentCorrelated))
	defer span.End()
	candidates := incidents.Correlate(valid)
	createdCount := 0
	for _, c := range candidates {
		inc, created, err := s.Store.UpsertIncident(c, now)
		if created {
			createdCount++
		}
		if err != nil {
			s.Logger.Error("incident upsert failed", "event", "incident.upsert.failed", "error", err, "occurrence_key", c.OccurrenceKey)
			continue
		}
		if created && s.Metrics != nil {
			mode := string(s.Config.EnvironmentMode)
			s.Metrics.IncidentsActive.WithLabelValues(mode, string(c.Severity)).Inc()
			// A newly-created occurrence with sequence > 1 is a recurrence:
			// this fault+device fired again after a prior occurrence was
			// resolved or ignored. This is exactly what
			// vyomm_incidents_recurred_total counts per METRICS_CONTRACT.md.
			if inc.OccurrenceSequence > 1 {
				s.Metrics.IncidentsRecurred.WithLabelValues(mode).Inc()
			}
		}
	}
	span.SetAttributes(attribute.Int("vyomm.incidents_created", createdCount))
}

func (s *Server) handleGetTelemetry(w http.ResponseWriter, r *http.Request) {
	devices := s.Store.LatestDevices()
	writeJSON(w, http.StatusOK, telemetryGetResponse{
		Devices:    devices,
		ServerTime: s.Clock.Now().Format(timeRFC3339),
		Mode:       string(s.Config.EnvironmentMode),
	})
}

func (s *Server) handleForecast(w http.ResponseWriter, r *http.Request) {
	device := r.URL.Query().Get("device")
	selected := device
	if selected == "" {
		selected = s.riskiestDevice()
	}
	var history []telemetry.DeviceTelemetry
	if selected != "" {
		history = s.Store.History(selected)
	}
	result := forecast.Build(selected, history)
	writeJSON(w, http.StatusOK, toForecastDTO(result, s.Config.EnvironmentMode, s.RunID, s.Clock.Now()))
}

// riskiestDevice picks the highest-risk currently-known device as the
// default forecast target when no device query parameter is supplied,
// mirroring the original Python behavior's _riskiest_device heuristic.
func (s *Server) riskiestDevice() string {
	devices := s.Store.LatestDevices()
	if len(devices) == 0 {
		return ""
	}
	best := devices[0]
	bestScore := riskScore(best)
	for _, d := range devices[1:] {
		if score := riskScore(d); score > bestScore {
			best, bestScore = d, score
		}
	}
	return best.Hostname
}

func riskScore(d telemetry.DeviceTelemetry) float64 {
	return d.CPUPercent*0.35 + d.LatencyMS*0.25 + d.PacketLossPercent*8 + d.MemoryPercent*0.2
}

func (s *Server) handleAnomalies(w http.ResponseWriter, r *http.Request) {
	anomalies := s.Store.Anomalies()
	out := make([]anomalyDTO, len(anomalies))
	for i, a := range anomalies {
		out[i] = toAnomalyDTO(a)
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleListIncidents(w http.ResponseWriter, r *http.Request) {
	items := s.Store.ListIncidents()
	itemDTOs := make([]incidentDTO, len(items))
	for i, inc := range items {
		itemDTOs[i] = toIncidentDTO(inc)
	}
	var activeDTO *incidentDTO
	if active, ok := s.Store.ActiveIncident(); ok {
		dto := toIncidentDTO(active)
		activeDTO = &dto
	}
	writeJSON(w, http.StatusOK, incidentsListResponse{Active: activeDTO, Items: itemDTOs})
}

// handleDecideIncident implements the "decision.recorded" step of the
// bounded workflow. Like handleRunbook, this starts a fresh root span
// rather than continuing the originating ingest trace, since the incident
// record does not yet carry a stored trace ID linking it back (tracked as
// a follow-up in docs/migration-plan.md, alongside simulator-side context
// propagation).
func (s *Server) handleDecideIncident(w http.ResponseWriter, r *http.Request) {
	_, span := tracer.Start(r.Context(), string(tracing.SpanDecisionRecorded))
	defer span.End()

	id := r.PathValue("id")
	span.SetAttributes(attribute.String("vyomm.incident_id", id))
	var req decisionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		span.SetStatus(codes.Error, "malformed request body")
		writeError(w, http.StatusBadRequest, "invalid_request", "malformed JSON body: "+err.Error())
		return
	}
	status := incidents.Status(req.Status)
	if status != incidents.StatusResolved && status != incidents.StatusIgnored {
		span.SetStatus(codes.Error, "invalid status")
		writeError(w, http.StatusBadRequest, "invalid_status", "status must be 'resolved' or 'ignored'")
		return
	}
	span.SetAttributes(attribute.String("vyomm.decision_status", string(status)))
	updated, wasActive, err := s.Store.DecideIncident(id, status, req.Actor, s.Clock.Now())
	if err != nil {
		span.SetStatus(codes.Error, "incident not found")
		writeError(w, http.StatusNotFound, "not_found", "incident not found: "+id)
		return
	}
	if s.Metrics != nil {
		mode := string(s.Config.EnvironmentMode)
		// Decrement the active gauge only when the incident actually left
		// the active state, so it mirrors the increment done at creation and
		// never goes negative if an already-decided incident is decided
		// again. Fixes the previously write-only (monotonically growing)
		// vyomm_incidents_active gauge.
		if wasActive {
			s.Metrics.IncidentsActive.WithLabelValues(mode, string(updated.Severity)).Dec()
		}
		if status == incidents.StatusResolved {
			s.Metrics.IncidentsResolved.WithLabelValues(mode, string(updated.Severity)).Inc()
		}
	}
	writeJSON(w, http.StatusOK, toIncidentDTO(updated))
}

func (s *Server) handleDecisions(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	decisions := s.Store.Decisions(id)
	out := make([]decisionDTO, len(decisions))
	for i, d := range decisions {
		out[i] = toDecisionDTO(d)
	}
	writeJSON(w, http.StatusOK, out)
}

// handleRunbook implements the "runbook.retrieved" step of the bounded
// workflow.
func (s *Server) handleRunbook(w http.ResponseWriter, r *http.Request) {
	_, span := tracer.Start(r.Context(), string(tracing.SpanRunbookRetrieved))
	defer span.End()

	query := r.URL.Query().Get("query")
	if query == "" {
		query = "network incident"
	}
	span.SetAttributes(attribute.String("vyomm.query", query))
	if s.Runbooks == nil {
		writeJSON(w, http.StatusOK, []runbookDTO{})
		return
	}
	results := s.Runbooks.Retrieve(query, 2)
	span.SetAttributes(attribute.Int("vyomm.results", len(results)))
	out := make([]runbookDTO, len(results))
	for i, res := range results {
		out[i] = toRunbookDTO(res)
	}
	writeJSON(w, http.StatusOK, out)
}

// Scenario execution (internal/scenarios) does not exist yet — these
// endpoints honestly report that rather than faking success, per the
// project rule against claiming success without a corresponding passing
// implementation.
func (s *Server) handleListScenarios(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, []struct{}{})
}

func (s *Server) handleRunScenario(w http.ResponseWriter, r *http.Request) {
	writeError(w, http.StatusNotImplemented, "not_implemented", "scenario execution is not implemented yet (internal/scenarios pending)")
}

func (s *Server) handleScenarioRun(w http.ResponseWriter, r *http.Request) {
	writeError(w, http.StatusNotImplemented, "not_implemented", "scenario run lookup is not implemented yet (internal/scenarios pending)")
}
