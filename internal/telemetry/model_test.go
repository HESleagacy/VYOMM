package telemetry

import (
	"testing"
	"time"
)

func validDevice() DeviceTelemetry {
	return DeviceTelemetry{
		Hostname:          "rtr-01",
		Role:              RoleRouter,
		CPUPercent:        42.0,
		MemoryPercent:     30.0,
		BandwidthPercent:  20.0,
		TemperatureC:      50.0,
		LatencyMS:         12.0,
		PacketLossPercent: 0.1,
		UptimeSeconds:     3600,
		Status:            StatusHealthy,
		ObservedAt:        time.Now().UTC(),
		Source:            SourceSynthetic,
		Mode:              ModeTrial,
	}
}

func TestDeviceTelemetry_Validate_OK(t *testing.T) {
	if err := validDevice().Validate(); err != nil {
		t.Fatalf("expected valid device, got error: %v", err)
	}
}

func TestDeviceTelemetry_Validate_Rejects(t *testing.T) {
	cases := map[string]func(d *DeviceTelemetry){
		"empty hostname":       func(d *DeviceTelemetry) { d.Hostname = "" },
		"invalid role":         func(d *DeviceTelemetry) { d.Role = "printer" },
		"invalid status":       func(d *DeviceTelemetry) { d.Status = "unknown" },
		"cpu too high":         func(d *DeviceTelemetry) { d.CPUPercent = 101 },
		"cpu negative":         func(d *DeviceTelemetry) { d.CPUPercent = -1 },
		"memory too high":      func(d *DeviceTelemetry) { d.MemoryPercent = 150 },
		"bandwidth negative":   func(d *DeviceTelemetry) { d.BandwidthPercent = -5 },
		"negative packet loss": func(d *DeviceTelemetry) { d.PacketLossPercent = -0.1 },
		"negative latency":     func(d *DeviceTelemetry) { d.LatencyMS = -1 },
		"zero observed_at":     func(d *DeviceTelemetry) { d.ObservedAt = time.Time{} },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			d := validDevice()
			mutate(&d)
			if err := d.Validate(); err == nil {
				t.Fatalf("expected validation error for case %q, got nil", name)
			}
		})
	}
}

func TestNewValue_PopulatesPointer(t *testing.T) {
	now := time.Now().UTC()
	v := NewValue(67.2, "percent", SourceSynthetic, ModeTrial, now, "run-1")
	if v.Value == nil || *v.Value != 67.2 {
		t.Fatalf("expected value pointer to 67.2, got %+v", v)
	}
	if v.UnavailableReason != "" {
		t.Errorf("expected empty unavailable reason, got %q", v.UnavailableReason)
	}
}

func TestUnavailable_HasNilValueAndReason(t *testing.T) {
	now := time.Now().UTC()
	v := Unavailable("percent", ModeTrial, now, "run-1", "Unavailable in simulated mode")
	if v.Value != nil {
		t.Fatalf("expected nil value pointer, got %v", *v.Value)
	}
	if v.UnavailableReason != "Unavailable in simulated mode" {
		t.Errorf("expected unavailable reason to be set, got %q", v.UnavailableReason)
	}
	if v.Source != SourceUnavailable {
		t.Errorf("expected source=unavailable, got %q", v.Source)
	}
}
