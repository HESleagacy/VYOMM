// Package forecast produces a short-horizon linear extrapolation of device
// telemetry. It is explicitly named and labeled as linear extrapolation —
// never as "Chronos-style" or any other model name VYOMM does not actually
// run. See API_CONTRACT.md's /api/v1/forecast contract.
package forecast

import (
	"math"

	"github.com/GrandRegentSarva/VYOMM/internal/telemetry"
)

// Method is the honest, fixed string reported to API clients. It must never
// be changed to imply a model VYOMM does not actually run.
const Method = "linear-extrapolation"

// HorizonMinutes is the total forecast horizon (6 points * 5 minutes).
const HorizonMinutes = 30

// RiskLevel mirrors the severity-like scale used for forecast risk.
type RiskLevel string

const (
	RiskLow      RiskLevel = "low"
	RiskMedium   RiskLevel = "medium"
	RiskHigh     RiskLevel = "high"
	RiskCritical RiskLevel = "critical"
)

// Point is one projected point on the forecast curve.
type Point struct {
	LabelMinutes      int     `json:"label_minutes"`
	CPUPercent        float64 `json:"cpu_percent"`
	LatencyMS         float64 `json:"latency_ms"`
	PacketLossPercent float64 `json:"packet_loss_percent"`
}

// Result is the full forecast response body (wrapped by the API layer with
// the provenance envelope for the Confidence field).
type Result struct {
	Device       string
	Method       string
	HorizonMins  int
	CurrentTrend string
	Confidence   float64
	RiskLevel    RiskLevel
	Points       []Point
}

// Build computes a linear-extrapolation forecast from recent history. It
// takes the most recent up-to-18 samples and projects CPU, latency, and
// packet loss forward using a simple least-recent-to-most-recent slope.
// Unlike the original Python implementation, this does NOT add a sine-wave
// term to manufacture a more "realistic" looking curve — the projection is
// a plain linear trend, honestly labeled as such.
func Build(device string, history []telemetry.DeviceTelemetry) Result {
	if device == "" || len(history) == 0 {
		return emptyResult()
	}

	recent := history
	if len(recent) > 18 {
		recent = recent[len(recent)-18:]
	}

	cpu := extractSeries(recent, func(d telemetry.DeviceTelemetry) float64 { return d.CPUPercent })
	latency := extractSeries(recent, func(d telemetry.DeviceTelemetry) float64 { return d.LatencyMS })
	loss := extractSeries(recent, func(d telemetry.DeviceTelemetry) float64 { return d.PacketLossPercent })

	points := make([]Point, 0, 6)
	for i := 1; i <= 6; i++ {
		points = append(points, Point{
			LabelMinutes:      i * 5,
			CPUPercent:        clamp(project(cpu, i), 0, 100),
			LatencyMS:         math.Max(0, project(latency, i)),
			PacketLossPercent: math.Max(0, project(loss, i)),
		})
	}

	terminal := points[len(points)-1]
	riskScore := terminal.CPUPercent*0.4 + terminal.LatencyMS*0.25 + terminal.PacketLossPercent*8
	risk := riskFromScore(riskScore)
	trend := trendFromRisk(risk)
	confidence := math.Min(95, 60+float64(len(recent))*1.5)

	return Result{
		Device:       device,
		Method:       Method,
		HorizonMins:  HorizonMinutes,
		CurrentTrend: trend,
		Confidence:   round1(confidence),
		RiskLevel:    risk,
		Points:       points,
	}
}

func emptyResult() Result {
	points := make([]Point, 0, 6)
	for i := 1; i <= 6; i++ {
		points = append(points, Point{LabelMinutes: i * 5})
	}
	return Result{
		Device:       "pending",
		Method:       Method,
		HorizonMins:  HorizonMinutes,
		CurrentTrend: "Awaiting telemetry",
		Confidence:   0,
		RiskLevel:    RiskLow,
		Points:       points,
	}
}

func extractSeries(history []telemetry.DeviceTelemetry, f func(telemetry.DeviceTelemetry) float64) []float64 {
	out := make([]float64, len(history))
	for i, d := range history {
		out[i] = f(d)
	}
	return out
}

// project extrapolates `steps` * 5-minute increments ahead using the slope
// between the first and last sample in the window. This is a plain linear
// trend — no periodic/sine adjustment is added.
func project(values []float64, steps int) float64 {
	if len(values) == 0 {
		return 0
	}
	if len(values) < 2 {
		return values[len(values)-1]
	}
	last := values[len(values)-1]
	slope := (last - values[0]) / float64(len(values)-1)
	return last + slope*float64(steps)
}

func clamp(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func riskFromScore(score float64) RiskLevel {
	switch {
	case score > 90:
		return RiskCritical
	case score > 72:
		return RiskHigh
	case score > 54:
		return RiskMedium
	default:
		return RiskLow
	}
}

func trendFromRisk(r RiskLevel) string {
	switch r {
	case RiskHigh, RiskCritical:
		return "Escalating"
	case RiskMedium:
		return "Stable with watch conditions"
	default:
		return "Healthy"
	}
}

func round1(v float64) float64 {
	return math.Round(v*10) / 10
}
