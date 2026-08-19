package api

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/GrandRegentSarva/VYOMM/internal/forecast"
	"github.com/GrandRegentSarva/VYOMM/internal/incidents"
	"github.com/GrandRegentSarva/VYOMM/internal/telemetry"
)

const timeRFC3339 = time.RFC3339

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	checks := map[string]string{
		"database":      "ok", // Store is constructed successfully at startup or the process would not be serving
		"llm_provider":  "not_configured",
		"otel_exporter": "disabled",
	}
	writeJSON(w, http.StatusOK, healthDTO{
		Status:  "ok",
		Mode:    string(s.Config.EnvironmentMode),
		Checks:  checks,
		Version: s.Version,
		RunID:   s.RunID,
	})
}

func (s *Server) handleIngestTelemetry(w http.ResponseWriter, r *http.Request) {
	var req telemetryIngestRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "malformed JSON body: "+err.Error())
		return
	}
	now := s.Clock.Now()
	result, err := s.Store.Ingest(req.Devices, req.RunID, now)
	if err != nil {
		s.Logger.Error("telemetry ingest failed", "event", "telemetry.ingest.failed", "error", err)
		writeError(w, http.StatusInternalServerError, "ingest_failed", "failed to persist telemetry batch")
		return
	}
	if s.Metrics != nil {
		mode := string(s.Config.EnvironmentMode)
		if result.Accepted > 0 {
			s.Metrics.TelemetryRecordsReceived.WithLabelValues(mode, "synthetic").Add(float64(result.Accepted))
		}
		if result.Rejected > 0 {
			s.Metrics.TelemetryIngestionErrors.WithLabelValues(mode, "validation").Add(float64(result.Rejected))
		}
	}
	s.correlateIncidents(req.Devices, now)
	writeJSON(w, http.StatusOK, telemetryIngestResponse{Accepted: result.Accepted, Rejected: result.Rejected, Errors: result.Errors})
}

// correlateIncidents runs the incident correlation rules against the
// ingested batch and upserts any matches, matching the original behavior
// of correlating on every ingest rather than on a separate poll loop.
func (s *Server) correlateIncidents(devices []telemetry.DeviceTelemetry, now time.Time) {
	valid := make([]telemetry.DeviceTelemetry, 0, len(devices))
	for _, d := range devices {
		if d.Validate() == nil {
			valid = append(valid, d)
		}
	}
	candidates := incidents.Correlate(valid)
	for _, c := range candidates {
		_, created, err := s.Store.UpsertIncident(c, now)
		if err != nil {
			s.Logger.Error("incident upsert failed", "event", "incident.upsert.failed", "error", err, "occurrence_key", c.OccurrenceKey)
			continue
		}
		if created && s.Metrics != nil {
			mode := string(s.Config.EnvironmentMode)
			s.Metrics.IncidentsActive.WithLabelValues(mode, string(c.Severity)).Inc()
		}
	}
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

func (s *Server) handleDecideIncident(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req decisionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "malformed JSON body: "+err.Error())
		return
	}
	status := incidents.Status(req.Status)
	if status != incidents.StatusResolved && status != incidents.StatusIgnored {
		writeError(w, http.StatusBadRequest, "invalid_status", "status must be 'resolved' or 'ignored'")
		return
	}
	updated, err := s.Store.DecideIncident(id, status, req.Actor, s.Clock.Now())
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found", "incident not found: "+id)
		return
	}
	if s.Metrics != nil && status == incidents.StatusResolved {
		s.Metrics.IncidentsResolved.WithLabelValues(string(s.Config.EnvironmentMode), string(updated.Severity)).Inc()
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

func (s *Server) handleRunbook(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query().Get("query")
	if query == "" {
		query = "network incident"
	}
	if s.Runbooks == nil {
		writeJSON(w, http.StatusOK, []runbookDTO{})
		return
	}
	results := s.Runbooks.Retrieve(query, 2)
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
