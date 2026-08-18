package detection

import (
	"testing"
	"time"

	"github.com/GrandRegentSarva/VYOMM/internal/telemetry"
)

func healthyDevice(hostname string) telemetry.DeviceTelemetry {
	return telemetry.DeviceTelemetry{
		Hostname:          hostname,
		Role:              telemetry.RoleRouter,
		CPUPercent:        40,
		MemoryPercent:     30,
		BandwidthPercent:  20,
		TemperatureC:      50,
		LatencyMS:         15,
		PacketLossPercent: 0.1,
		UptimeSeconds:     100,
		Status:            telemetry.StatusHealthy,
		ObservedAt:        time.Now().UTC(),
		Source:            telemetry.SourceSynthetic,
		Mode:              telemetry.ModeTrial,
	}
}

func TestDetect_NoAnomaliesForHealthyDevice(t *testing.T) {
	now := time.Now().UTC()
	got := Detect([]telemetry.DeviceTelemetry{healthyDevice("rtr-01")}, now)
	if len(got) != 0 {
		t.Fatalf("expected no anomalies for healthy device, got %d: %+v", len(got), got)
	}
}

func TestDetect_CriticalCPU(t *testing.T) {
	d := healthyDevice("rtr-01")
	d.CPUPercent = 99
	now := time.Now().UTC()
	got := Detect([]telemetry.DeviceTelemetry{d}, now)
	if len(got) != 1 {
		t.Fatalf("expected exactly 1 anomaly, got %d: %+v", len(got), got)
	}
	if got[0].Signal != "cpu_saturation" {
		t.Errorf("expected cpu_saturation signal, got %q", got[0].Signal)
	}
	if got[0].Severity != SeverityCritical {
		t.Errorf("expected critical severity for cpu=99, got %q", got[0].Severity)
	}
}

func TestDetect_MultipleSignalsOnOneDevice(t *testing.T) {
	d := healthyDevice("rtr-01")
	d.CPUPercent = 99
	d.MemoryPercent = 97
	d.LatencyMS = 200
	d.PacketLossPercent = 9
	d.TemperatureC = 90
	now := time.Now().UTC()
	got := Detect([]telemetry.DeviceTelemetry{d}, now)
	if len(got) != 5 {
		t.Fatalf("expected 5 anomalies (one per signal), got %d: %+v", len(got), got)
	}
}

func TestDetect_DeduplicatesWithinTimeBucket(t *testing.T) {
	d := healthyDevice("rtr-01")
	d.CPUPercent = 99
	base := time.Unix(1000000000, 0).UTC()
	first := Detect([]telemetry.DeviceTelemetry{d}, base)
	second := Detect([]telemetry.DeviceTelemetry{d}, base.Add(5*time.Second))
	if len(first) != 1 || len(second) != 1 {
		t.Fatalf("expected 1 anomaly each call, got %d and %d", len(first), len(second))
	}
	if first[0].ID != second[0].ID {
		t.Errorf("expected same anomaly ID within the same detection window, got %q vs %q", first[0].ID, second[0].ID)
	}
}

func TestDetect_NewIDAcrossTimeBuckets(t *testing.T) {
	d := healthyDevice("rtr-01")
	d.CPUPercent = 99
	base := time.Unix(1000000000, 0).UTC()
	first := Detect([]telemetry.DeviceTelemetry{d}, base)
	later := Detect([]telemetry.DeviceTelemetry{d}, base.Add(60*time.Second))
	if first[0].ID == later[0].ID {
		t.Errorf("expected different anomaly IDs across detection windows, got same ID %q", first[0].ID)
	}
}

func TestDetect_ScoreCappedAtOne(t *testing.T) {
	d := healthyDevice("rtr-01")
	d.PacketLossPercent = 50 // far beyond critical=8
	now := time.Now().UTC()
	got := Detect([]telemetry.DeviceTelemetry{d}, now)
	if len(got) != 1 {
		t.Fatalf("expected 1 anomaly, got %d", len(got))
	}
	if got[0].Score != 1 {
		t.Errorf("expected score capped at 1.0, got %v", got[0].Score)
	}
}
