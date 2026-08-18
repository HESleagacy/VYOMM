// Package api implements the HTTP surface described in API_CONTRACT.md.
// All wire-format field names and shapes live in this file as explicit DTOs
// — internal domain types (internal/telemetry, internal/forecast,
// internal/incidents, internal/detection, internal/runbooks) are kept
// independent of the wire contract so their tests stay focused on
// behavior, not JSON shape.
package api

import (
	"time"

	"github.com/GrandRegentSarva/VYOMM/internal/config"
	"github.com/GrandRegentSarva/VYOMM/internal/detection"
	"github.com/GrandRegentSarva/VYOMM/internal/forecast"
	"github.com/GrandRegentSarva/VYOMM/internal/incidents"
	"github.com/GrandRegentSarva/VYOMM/internal/runbooks"
	"github.com/GrandRegentSarva/VYOMM/internal/telemetry"
)

// provenanceDTO is the mandatory envelope from API_CONTRACT.md. Value is a
// pointer so a null JSON value is emitted when a measurement is unavailable
// in the current mode, per the contract's explicit rule against
// manufacturing plausible-looking numbers.
type provenanceDTO struct {
	Value             *float64 `json:"value"`
	Unit              string   `json:"unit"`
	Source            string   `json:"source"`
	Mode              string   `json:"mode"`
	ObservedAt        string   `json:"observed_at"`
	RunID             string   `json:"run_id"`
	UnavailableReason string   `json:"unavailable_reason,omitempty"`
}

func toProvenanceDTO(v telemetry.Value) provenanceDTO {
	return provenanceDTO{
		Value:             v.Value,
		Unit:              v.Unit,
		Source:            string(v.Source),
		Mode:              string(v.Mode),
		ObservedAt:        v.ObservedAt.UTC().Format(time.RFC3339),
		RunID:             v.RunID,
		UnavailableReason: v.UnavailableReason,
	}
}

type healthDTO struct {
	Status  string            `json:"status"`
	Mode    string            `json:"mode"`
	Checks  map[string]string `json:"checks"`
	Version string            `json:"version"`
	RunID   string            `json:"run_id"`
}

type telemetryIngestRequest struct {
	RunID      string                      `json:"run_id"`
	ScenarioID string                      `json:"scenario_id"`
	Devices    []telemetry.DeviceTelemetry `json:"devices"`
	Logs       []string                    `json:"logs"`
}

type telemetryIngestResponse struct {
	Accepted int      `json:"accepted"`
	Rejected int      `json:"rejected"`
	Errors   []string `json:"errors"`
}

type telemetryGetResponse struct {
	Devices    []telemetry.DeviceTelemetry `json:"devices"`
	ServerTime string                      `json:"server_time"`
	Mode       string                      `json:"mode"`
}

type forecastPointDTO struct {
	Label             string  `json:"label"`
	CPUPercent        float64 `json:"cpu_percent"`
	LatencyMS         float64 `json:"latency_ms"`
	PacketLossPercent float64 `json:"packet_loss_percent"`
}

type forecastDTO struct {
	Device       string             `json:"device"`
	Method       string             `json:"method"`
	HorizonMins  int                `json:"horizon_minutes"`
	CurrentTrend string             `json:"current_trend"`
	Confidence   provenanceDTO      `json:"confidence"`
	RiskLevel    string             `json:"risk_level"`
	Points       []forecastPointDTO `json:"points"`
}

func toForecastDTO(r forecast.Result, mode config.EnvironmentMode, runID string, now time.Time) forecastDTO {
	points := make([]forecastPointDTO, len(r.Points))
	for i, p := range r.Points {
		points[i] = forecastPointDTO{
			Label:             formatLabel(p.LabelMinutes),
			CPUPercent:        p.CPUPercent,
			LatencyMS:         p.LatencyMS,
			PacketLossPercent: p.PacketLossPercent,
		}
	}
	confidence := telemetry.NewValue(r.Confidence, "percent", telemetry.SourceComputed, telemetry.Mode(mode), now, runID)
	return forecastDTO{
		Device:       r.Device,
		Method:       r.Method,
		HorizonMins:  r.HorizonMins,
		CurrentTrend: r.CurrentTrend,
		Confidence:   toProvenanceDTO(confidence),
		RiskLevel:    string(r.RiskLevel),
		Points:       points,
	}
}

func formatLabel(minutes int) string {
	return "+" + itoa(minutes) + "m"
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

type anomalyDTO struct {
	ID         string  `json:"id"`
	Device     string  `json:"device"`
	Signal     string  `json:"signal"`
	Score      float64 `json:"score"`
	Severity   string  `json:"severity"`
	DetectedAt string  `json:"detected_at"`
}

func toAnomalyDTO(a detection.Anomaly) anomalyDTO {
	return anomalyDTO{
		ID:         a.ID,
		Device:     a.Device,
		Signal:     a.Signal,
		Score:      a.Score,
		Severity:   string(a.Severity),
		DetectedAt: a.DetectedAt.UTC().Format(time.RFC3339),
	}
}

type incidentDTO struct {
	ID                        string   `json:"id"`
	OccurrenceKey             string   `json:"occurrence_key"`
	OccurrenceSequence        int      `json:"occurrence_sequence"`
	Severity                  string   `json:"severity"`
	AffectedDevices           []string `json:"affected_devices"`
	RootCause                 string   `json:"root_cause"`
	Status                    string   `json:"status"`
	CreatedAt                 string   `json:"created_at"`
	UpdatedAt                 string   `json:"updated_at"`
	Confidence                float64  `json:"confidence"`
	PredictedSLABreachMinutes int      `json:"predicted_sla_breach_minutes"`
	RecommendedAction         string   `json:"recommended_action"`
}

func toIncidentDTO(i incidents.Incident) incidentDTO {
	return incidentDTO{
		ID:                        i.ID,
		OccurrenceKey:             i.OccurrenceKey,
		OccurrenceSequence:        i.OccurrenceSequence,
		Severity:                  string(i.Severity),
		AffectedDevices:           i.AffectedDevices,
		RootCause:                 i.RootCause,
		Status:                    string(i.Status),
		CreatedAt:                 i.CreatedAt.UTC().Format(time.RFC3339),
		UpdatedAt:                 i.UpdatedAt.UTC().Format(time.RFC3339),
		Confidence:                i.Confidence,
		PredictedSLABreachMinutes: i.PredictedSLABreachMinutes,
		RecommendedAction:         i.RecommendedAction,
	}
}

type incidentsListResponse struct {
	Active *incidentDTO  `json:"active"`
	Items  []incidentDTO `json:"items"`
}

type decisionRequest struct {
	Status string `json:"status"`
	Actor  string `json:"actor"`
}

type decisionDTO struct {
	Status    string `json:"status"`
	Actor     string `json:"actor"`
	DecidedAt string `json:"decided_at"`
}

func toDecisionDTO(d incidents.Decision) decisionDTO {
	return decisionDTO{
		Status:    string(d.Status),
		Actor:     d.Actor,
		DecidedAt: d.DecidedAt.UTC().Format(time.RFC3339),
	}
}

type runbookDTO struct {
	Title       string `json:"title"`
	Source      string `json:"source"`
	Content     string `json:"content"`
	MatchMethod string `json:"match_method"`
}

func toRunbookDTO(r runbooks.Result) runbookDTO {
	return runbookDTO{Title: r.Title, Source: r.Source, Content: r.Content, MatchMethod: r.MatchMethod}
}

type errorDTO struct {
	Error errorBody `json:"error"`
}

type errorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	TraceID string `json:"trace_id"`
}
