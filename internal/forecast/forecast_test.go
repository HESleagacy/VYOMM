package forecast

import (
	"testing"
	"time"

	"github.com/GrandRegentSarva/VYOMM/internal/telemetry"
)

func sampleAt(hostname string, cpu, latency, loss float64, t time.Time) telemetry.DeviceTelemetry {
	return telemetry.DeviceTelemetry{
		Hostname:          hostname,
		Role:              telemetry.RoleRouter,
		CPUPercent:        cpu,
		LatencyMS:         latency,
		PacketLossPercent: loss,
		Status:            telemetry.StatusHealthy,
		ObservedAt:        t,
		Source:            telemetry.SourceSynthetic,
		Mode:              telemetry.ModeTrial,
	}
}

func TestBuild_EmptyHistoryReturnsPendingResult(t *testing.T) {
	r := Build("rtr-01", nil)
	if r.Device != "pending" {
		t.Errorf("expected device=pending for empty history, got %q", r.Device)
	}
	if r.Method != Method {
		t.Errorf("expected method %q, got %q", Method, r.Method)
	}
	if len(r.Points) != 6 {
		t.Errorf("expected 6 forecast points, got %d", len(r.Points))
	}
}

func TestBuild_MethodIsHonestlyLabeled(t *testing.T) {
	history := []telemetry.DeviceTelemetry{sampleAt("rtr-01", 50, 20, 0.1, time.Now())}
	r := Build("rtr-01", history)
	if r.Method != "linear-extrapolation" {
		t.Fatalf("forecast method must be honestly labeled linear-extrapolation, got %q", r.Method)
	}
}

func TestBuild_HorizonIsThirtyMinutesAcrossSixPoints(t *testing.T) {
	history := []telemetry.DeviceTelemetry{sampleAt("rtr-01", 50, 20, 0.1, time.Now())}
	r := Build("rtr-01", history)
	if r.HorizonMins != 30 {
		t.Errorf("expected 30 minute horizon, got %d", r.HorizonMins)
	}
	if len(r.Points) != 6 {
		t.Fatalf("expected 6 points, got %d", len(r.Points))
	}
	for i, p := range r.Points {
		want := (i + 1) * 5
		if p.LabelMinutes != want {
			t.Errorf("point %d: expected label_minutes=%d, got %d", i, want, p.LabelMinutes)
		}
	}
}

func TestBuild_RisingTrendProjectsUpward(t *testing.T) {
	now := time.Now()
	var history []telemetry.DeviceTelemetry
	for i := 0; i < 10; i++ {
		history = append(history, sampleAt("rtr-01", float64(30+i*3), float64(10+i), 0.1, now.Add(time.Duration(i)*time.Second)))
	}
	r := Build("rtr-01", history)
	if r.Points[0].CPUPercent <= 57 { // last observed value was 30+9*3=57
		t.Errorf("expected forecast to project upward from last CPU=57, got first point %v", r.Points[0].CPUPercent)
	}
}

func TestBuild_SteepTrendCrossesHighRiskThreshold(t *testing.T) {
	// Verified by hand: terminal CPU clamps to 100, terminal latency ~146.4,
	// giving riskScore = 100*0.4 + 146.4*0.25 + 0.1*8 = 77.4, which is > 72
	// (the "high" threshold) and <= 90 (the "critical" threshold).
	now := time.Now()
	var history []telemetry.DeviceTelemetry
	for i := 0; i < 10; i++ {
		history = append(history, sampleAt("rtr-01", 70+float64(i)*2.78, 80+float64(i)*4.4, 0.1, now.Add(time.Duration(i)*time.Second)))
	}
	r := Build("rtr-01", history)
	if r.RiskLevel != RiskHigh {
		t.Errorf("expected high risk for steep CPU+latency trend, got %q", r.RiskLevel)
	}
	if r.CurrentTrend != "Escalating" {
		t.Errorf("expected Escalating trend for high risk, got %q", r.CurrentTrend)
	}
}

func TestBuild_CPUNeverExceeds100(t *testing.T) {
	now := time.Now()
	var history []telemetry.DeviceTelemetry
	for i := 0; i < 10; i++ {
		history = append(history, sampleAt("rtr-01", float64(90+i), 10, 0.1, now.Add(time.Duration(i)*time.Second)))
	}
	r := Build("rtr-01", history)
	for _, p := range r.Points {
		if p.CPUPercent > 100 {
			t.Errorf("expected CPU forecast clamped to 100, got %v", p.CPUPercent)
		}
	}
}

func TestBuild_FlatHistoryIsStable(t *testing.T) {
	now := time.Now()
	var history []telemetry.DeviceTelemetry
	for i := 0; i < 5; i++ {
		history = append(history, sampleAt("rtr-01", 40, 15, 0.1, now.Add(time.Duration(i)*time.Second)))
	}
	r := Build("rtr-01", history)
	if r.RiskLevel != RiskLow {
		t.Errorf("expected low risk for flat healthy history, got %q", r.RiskLevel)
	}
	if r.CurrentTrend != "Healthy" {
		t.Errorf("expected Healthy trend for flat history, got %q", r.CurrentTrend)
	}
}

func TestBuild_Deterministic(t *testing.T) {
	now := time.Now()
	var history []telemetry.DeviceTelemetry
	for i := 0; i < 8; i++ {
		history = append(history, sampleAt("rtr-01", float64(50+i), float64(20+i), 0.2, now.Add(time.Duration(i)*time.Second)))
	}
	r1 := Build("rtr-01", history)
	r2 := Build("rtr-01", history)
	if r1.Points[0].CPUPercent != r2.Points[0].CPUPercent || r1.Confidence != r2.Confidence {
		t.Fatalf("expected deterministic forecast for identical input, got %v vs %v", r1, r2)
	}
}
