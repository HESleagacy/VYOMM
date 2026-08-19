// Package detection implements threshold-based anomaly detection over
// device telemetry. This is an honest port of static-threshold logic; it
// does not claim to be machine learning or baselining, and callers must not
// present it as more sophisticated than it is.
package detection

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/GrandRegentSarva/VYOMM/internal/telemetry"
)

// Severity mirrors the incident/anomaly severity scale used across VYOMM.
type Severity string

const (
	SeverityLow      Severity = "low"
	SeverityMedium   Severity = "medium"
	SeverityHigh     Severity = "high"
	SeverityCritical Severity = "critical"
)

// Anomaly is one detected threshold breach for one device/signal.
type Anomaly struct {
	ID         string
	Score      float64
	Severity   Severity
	Device     string
	Signal     string
	DetectedAt time.Time
}

// signalCheck defines one threshold rule: a signal name, the value it reads
// off the device, and the warning/critical thresholds.
type signalCheck struct {
	name     string
	value    func(telemetry.DeviceTelemetry) float64
	warning  float64
	critical float64
}

var checks = []signalCheck{
	{name: "cpu_saturation", value: func(d telemetry.DeviceTelemetry) float64 { return d.CPUPercent }, warning: 88, critical: 97},
	{name: "memory_pressure", value: func(d telemetry.DeviceTelemetry) float64 { return d.MemoryPercent }, warning: 86, critical: 96},
	{name: "high_latency", value: func(d telemetry.DeviceTelemetry) float64 { return d.LatencyMS }, warning: 85, critical: 150},
	{name: "packet_loss", value: func(d telemetry.DeviceTelemetry) float64 { return d.PacketLossPercent }, warning: 3.5, critical: 8},
	{name: "thermal_drift", value: func(d telemetry.DeviceTelemetry) float64 { return d.TemperatureC }, warning: 72, critical: 86},
}

// DetectionWindowSeconds controls the time-bucketing used for anomaly ID
// deduplication: repeated breaches of the same signal on the same device
// within one window collapse to the same anomaly ID rather than creating
// duplicate entries every tick.
const DetectionWindowSeconds = 20

// Detect evaluates every configured threshold check against every device
// and returns one Anomaly per breach. now is passed explicitly (not
// time.Now()) so tests are deterministic.
func Detect(devices []telemetry.DeviceTelemetry, now time.Time) []Anomaly {
	var out []Anomaly
	bucket := now.Unix() / DetectionWindowSeconds
	for _, d := range devices {
		for _, c := range checks {
			v := c.value(d)
			if v < c.warning {
				continue
			}
			score := v / c.critical
			if score > 1 {
				score = 1
			}
			severity := SeverityMedium
			switch {
			case v >= c.critical:
				severity = SeverityCritical
			case score > 0.82:
				severity = SeverityHigh
			}
			id := anomalyID(d.Hostname, c.name, bucket)
			out = append(out, Anomaly{
				ID:         id,
				Score:      round3(score),
				Severity:   severity,
				Device:     d.Hostname,
				Signal:     c.name,
				DetectedAt: now,
			})
		}
	}
	return out
}

func anomalyID(hostname, signal string, bucket int64) string {
	h := sha1.Sum([]byte(fmt.Sprintf("%s:%s:%d", hostname, signal, bucket)))
	return "anom-" + hex.EncodeToString(h[:])[:10]
}

func round3(v float64) float64 {
	return float64(int(v*1000+0.5)) / 1000
}
