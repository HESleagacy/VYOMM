// Package incidents implements fault correlation across device telemetry
// and incident lifecycle management with recurrence support.
//
// This is a deliberate fix of a real defect found in the original Python
// implementation (see docs/migration-plan.md): there, an incident's ID was
// a deterministic hash of (fault type, hostname), so once an incident was
// resolved it could never fire again for the same device+fault — the
// correlation code returned early whenever an existing (by that same
// deterministic ID) incident existed, regardless of status.
//
// Here, every occurrence gets a unique ID, but occurrences of the same
// underlying fault+device are linked by a stable OccurrenceKey with an
// incrementing OccurrenceSequence, so:
//   - a currently-active occurrence is not duplicated (still deduplicated),
//   - a resolved/ignored occurrence CAN produce a new active occurrence,
//   - full history is preserved via the OccurrenceKey linkage.
package incidents

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/GrandRegentSarva/VYOMM/internal/telemetry"
)

type Severity string

const (
	SeverityLow      Severity = "low"
	SeverityMedium   Severity = "medium"
	SeverityHigh     Severity = "high"
	SeverityCritical Severity = "critical"
)

type Status string

const (
	StatusActive   Status = "active"
	StatusResolved Status = "resolved"
	StatusIgnored  Status = "ignored"
)

// Incident is one occurrence of a correlated fault.
type Incident struct {
	ID                        string
	OccurrenceKey             string
	OccurrenceSequence        int
	Severity                  Severity
	AffectedDevices           []string
	RootCause                 string
	Status                    Status
	CreatedAt                 time.Time
	UpdatedAt                 time.Time
	Confidence                float64
	PredictedSLABreachMinutes int
	RecommendedAction         string
}

// Decision is one recorded human decision against an incident, forming the
// audit trail required by API_CONTRACT.md's
// GET /api/v1/incidents/{id}/decisions.
type Decision struct {
	IncidentID string
	Status     Status
	Actor      string
	DecidedAt  time.Time
}

// rule defines one correlation rule matched against a device (with its
// resolved neighbors). Rules are evaluated in a fixed order; the first
// match wins per device, matching the original Python cascade semantics.
type rule struct {
	rootCause string
	severity  Severity
	slaMins   int
	action    string
	matches   func(d telemetry.DeviceTelemetry, neighbors []telemetry.DeviceTelemetry) bool
}

func maxLatency(neighbors []telemetry.DeviceTelemetry, fallback float64) float64 {
	if len(neighbors) == 0 {
		return fallback
	}
	m := neighbors[0].LatencyMS
	for _, n := range neighbors[1:] {
		if n.LatencyMS > m {
			m = n.LatencyMS
		}
	}
	return m
}

func maxLoss(neighbors []telemetry.DeviceTelemetry, fallback float64) float64 {
	if len(neighbors) == 0 {
		return fallback
	}
	m := neighbors[0].PacketLossPercent
	for _, n := range neighbors[1:] {
		if n.PacketLossPercent > m {
			m = n.PacketLossPercent
		}
	}
	return m
}

var rules = []rule{
	{
		rootCause: "Congestion Incident",
		severity:  SeverityCritical,
		slaMins:   22,
		action:    "Drain traffic from the affected path and apply QoS shaping.",
		matches: func(d telemetry.DeviceTelemetry, n []telemetry.DeviceTelemetry) bool {
			return d.Role == telemetry.RoleRouter && d.CPUPercent > 95 && maxLatency(n, d.LatencyMS) > 100 && maxLoss(n, d.PacketLossPercent) > 5
		},
	},
	{
		rootCause: "Firewall Saturation",
		severity:  SeverityHigh,
		slaMins:   18,
		action:    "Move inspection-heavy flows to standby firewall and verify policy hit counters.",
		matches: func(d telemetry.DeviceTelemetry, n []telemetry.DeviceTelemetry) bool {
			return d.Role == telemetry.RoleFirewall && (d.CPUPercent > 90 || d.BandwidthPercent > 92) && d.LatencyMS > 90
		},
	},
	{
		rootCause: "Memory Leak",
		severity:  SeverityHigh,
		slaMins:   22,
		action:    "Fail over services, collect process table, and restart the affected network daemon.",
		matches: func(d telemetry.DeviceTelemetry, n []telemetry.DeviceTelemetry) bool {
			return d.MemoryPercent > 94 && d.CPUPercent > 80
		},
	},
	{
		rootCause: "Packet Loss Degradation",
		severity:  SeverityHigh,
		slaMins:   22,
		action:    "Check interface errors, optical levels, and upstream queue drops.",
		matches: func(d telemetry.DeviceTelemetry, n []telemetry.DeviceTelemetry) bool {
			return d.PacketLossPercent > 7 && d.LatencyMS > 120
		},
	},
	{
		rootCause: "Switch Overheating",
		severity:  SeverityHigh,
		slaMins:   12,
		action:    "Shift access load, validate fan telemetry, and inspect rack airflow.",
		matches: func(d telemetry.DeviceTelemetry, n []telemetry.DeviceTelemetry) bool {
			return d.TemperatureC > 84 && d.Role == telemetry.RoleSwitch
		},
	},
	{
		rootCause: "BGP Edge Instability",
		severity:  SeverityCritical,
		slaMins:   15,
		action:    "Prefer secondary edge route, verify BGP timers, and compare route dampening events.",
		matches: func(d telemetry.DeviceTelemetry, n []telemetry.DeviceTelemetry) bool {
			return d.Role == telemetry.RoleGateway && d.LatencyMS > 140 && d.PacketLossPercent > 4
		},
	},
}

// Correlate evaluates every rule against every device (with its neighbors
// resolved from the full device set) and returns one Candidate per match.
// Correlate itself does not know about persisted incident state — that
// merge/recurrence logic lives in the Store, so this function stays a
// pure, easily-testable function of telemetry in, candidates out.
type Candidate struct {
	OccurrenceKey     string
	RootCause         string
	Severity          Severity
	AffectedDevices   []string
	Confidence        float64
	SLABreachMinutes  int
	RecommendedAction string
}

func Correlate(devices []telemetry.DeviceTelemetry) []Candidate {
	byHost := make(map[string]telemetry.DeviceTelemetry, len(devices))
	for _, d := range devices {
		byHost[d.Hostname] = d
	}

	var out []Candidate
	for _, d := range devices {
		neighbors := resolveNeighbors(d, byHost)
		for _, r := range rules {
			if !r.matches(d, neighbors) {
				continue
			}
			affected := []string{d.Hostname}
			for i, n := range neighbors {
				if i >= 2 {
					break
				}
				affected = append(affected, n.Hostname)
			}
			affected = dedupe(affected)
			confidence := 0.78 + minF(0.18, (d.CPUPercent+d.LatencyMS/2+d.PacketLossPercent*6)/1000)
			out = append(out, Candidate{
				OccurrenceKey:     occurrenceKey(r.rootCause, d.Hostname),
				RootCause:         r.rootCause,
				Severity:          r.severity,
				AffectedDevices:   affected,
				Confidence:        round2(confidence),
				SLABreachMinutes:  r.slaMins,
				RecommendedAction: r.action,
			})
			break // first matching rule wins per device, like the original cascade
		}
	}
	return out
}

func dedupe(in []string) []string {
	seen := make(map[string]bool, len(in))
	out := make([]string, 0, len(in))
	for _, v := range in {
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	return out
}

func minF(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}

func round2(v float64) float64 {
	return float64(int(v*100+0.5)) / 100
}

// resolveNeighbors mirrors the original Python's index-adjacency heuristic:
// devices whose numeric suffix is within 1 of the target's are treated as
// topologically adjacent, across router/switch/firewall/gateway prefixes.
func resolveNeighbors(d telemetry.DeviceTelemetry, byHost map[string]telemetry.DeviceTelemetry) []telemetry.DeviceTelemetry {
	index := numericSuffix(d.Hostname)
	prefixes := []string{"rtr", "sw", "fw", "gw"}
	var out []telemetry.DeviceTelemetry
	seen := map[string]bool{d.Hostname: true}
	for _, prefix := range prefixes {
		for _, offset := range []int{0, 1, -1} {
			candidateIndex := index + offset
			if candidateIndex < 1 {
				candidateIndex = 1
			}
			name := fmt.Sprintf("%s-%02d", prefix, candidateIndex)
			if seen[name] {
				continue
			}
			if n, ok := byHost[name]; ok {
				out = append(out, n)
				seen[name] = true
			}
		}
	}
	return out
}

func numericSuffix(hostname string) int {
	n := 0
	found := false
	for _, r := range hostname {
		if r >= '0' && r <= '9' {
			n = n*10 + int(r-'0')
			found = true
		}
	}
	if !found {
		return 1
	}
	return n
}

func occurrenceKey(rootCause, hostname string) string {
	return rootCause + ":" + hostname
}

// NewIncidentID generates a globally unique ID for one occurrence, distinct
// from the OccurrenceKey used to link recurrences together. sequence is the
// 1-based occurrence number for this OccurrenceKey.
func NewIncidentID(occurrenceKey string, sequence int, now time.Time) string {
	h := sha1.Sum([]byte(fmt.Sprintf("%s:%d:%d", occurrenceKey, sequence, now.UnixNano())))
	return fmt.Sprintf("INC-%s-%06d", hex.EncodeToString(h[:])[:6], sequence)
}
