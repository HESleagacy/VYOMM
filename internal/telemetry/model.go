// Package telemetry defines the core device telemetry types and the
// mandatory provenance envelope described in API_CONTRACT.md. No value that
// could be mistaken for real hardware telemetry may be represented without
// this envelope.
package telemetry

import (
	"fmt"
	"time"
)

// Source identifies where a value actually came from. It must never be
// fabricated: "synthetic" for simulator-generated data, "mock" for
// nvml-mock-derived data, "real" only for a verified physical source,
// "computed" for values honestly derived from other data (e.g. a linear
// forecast), and "unavailable" when the mode cannot supply the value at all.
type Source string

const (
	SourceSynthetic   Source = "synthetic"
	SourceMock        Source = "mock"
	SourceReal        Source = "real"
	SourceComputed    Source = "computed"
	SourceUnavailable Source = "unavailable"
)

// Mode mirrors config.EnvironmentMode but is kept independent in this
// package to avoid an import cycle between telemetry and config.
type Mode string

const (
	ModeTrial    Mode = "trial"
	ModeNVMLMock Mode = "nvml-mock"
	ModeRealGPU  Mode = "real-gpu"
)

// Value is the mandatory provenance envelope for any displayed number.
// UnavailableReason is set (and Value left nil) instead of manufacturing a
// realistic-looking number when a physical measurement cannot be produced
// in the current mode.
type Value struct {
	Value             *float64  `json:"value"`
	Unit              string    `json:"unit"`
	Source            Source    `json:"source"`
	Mode              Mode      `json:"mode"`
	ObservedAt        time.Time `json:"observed_at"`
	RunID             string    `json:"run_id"`
	UnavailableReason string    `json:"unavailable_reason,omitempty"`
}

// NewValue constructs a populated provenance-tagged value.
func NewValue(v float64, unit string, source Source, mode Mode, observedAt time.Time, runID string) Value {
	return Value{Value: &v, Unit: unit, Source: source, Mode: mode, ObservedAt: observedAt, RunID: runID}
}

// Unavailable constructs a provenance-tagged value explicitly marked as not
// obtainable in the current mode, per API_CONTRACT.md's provenance rule.
func Unavailable(unit string, mode Mode, observedAt time.Time, runID, reason string) Value {
	return Value{Value: nil, Unit: unit, Source: SourceUnavailable, Mode: mode, ObservedAt: observedAt, RunID: runID, UnavailableReason: reason}
}

// DeviceRole enumerates the simulated network device roles.
type DeviceRole string

const (
	RoleRouter   DeviceRole = "router"
	RoleSwitch   DeviceRole = "switch"
	RoleFirewall DeviceRole = "firewall"
	RoleGateway  DeviceRole = "gateway"
)

func (r DeviceRole) Valid() bool {
	switch r {
	case RoleRouter, RoleSwitch, RoleFirewall, RoleGateway:
		return true
	default:
		return false
	}
}

// HealthStatus enumerates device health classifications.
type HealthStatus string

const (
	StatusHealthy  HealthStatus = "healthy"
	StatusWarning  HealthStatus = "warning"
	StatusCritical HealthStatus = "critical"
)

func (s HealthStatus) Valid() bool {
	switch s {
	case StatusHealthy, StatusWarning, StatusCritical:
		return true
	default:
		return false
	}
}

// DeviceTelemetry is one device's telemetry sample. It carries Source/Mode
// directly (rather than wrapping every field in a Value envelope) because
// the whole sample shares one provenance; per-field Value envelopes are
// used for derived/aggregate metrics (forecast points, evaluation results,
// dashboard cards) where provenance can legitimately differ field to field.
type DeviceTelemetry struct {
	Hostname          string       `json:"hostname"`
	Role              DeviceRole   `json:"role"`
	CPUPercent        float64      `json:"cpu_percent"`
	MemoryPercent     float64      `json:"memory_percent"`
	BandwidthPercent  float64      `json:"bandwidth_percent"`
	TemperatureC      float64      `json:"temperature_c"`
	LatencyMS         float64      `json:"latency_ms"`
	PacketLossPercent float64      `json:"packet_loss_percent"`
	UptimeSeconds     int64        `json:"uptime_seconds"`
	Status            HealthStatus `json:"status"`
	ObservedAt        time.Time    `json:"observed_at"`
	Source            Source       `json:"source"`
	Mode              Mode         `json:"mode"`
}

// Validate performs the range/enum checks the old Python pydantic models
// used to do (CPU/memory/bandwidth in [0,100], packet loss >= 0), plus
// enum validation, so invalid ingested rows are rejected explicitly rather
// than silently stored or silently dropped without a reason.
func (d DeviceTelemetry) Validate() error {
	if d.Hostname == "" {
		return fmt.Errorf("hostname must not be empty")
	}
	if !d.Role.Valid() {
		return fmt.Errorf("invalid role %q", d.Role)
	}
	if !d.Status.Valid() {
		return fmt.Errorf("invalid status %q", d.Status)
	}
	if d.CPUPercent < 0 || d.CPUPercent > 100 {
		return fmt.Errorf("cpu_percent %.2f out of range [0,100]", d.CPUPercent)
	}
	if d.MemoryPercent < 0 || d.MemoryPercent > 100 {
		return fmt.Errorf("memory_percent %.2f out of range [0,100]", d.MemoryPercent)
	}
	if d.BandwidthPercent < 0 || d.BandwidthPercent > 100 {
		return fmt.Errorf("bandwidth_percent %.2f out of range [0,100]", d.BandwidthPercent)
	}
	if d.PacketLossPercent < 0 {
		return fmt.Errorf("packet_loss_percent %.2f must be >= 0", d.PacketLossPercent)
	}
	if d.LatencyMS < 0 {
		return fmt.Errorf("latency_ms %.2f must be >= 0", d.LatencyMS)
	}
	if d.ObservedAt.IsZero() {
		return fmt.Errorf("observed_at must be set")
	}
	return nil
}
