package incidents

import (
	"testing"
	"time"

	"github.com/GrandRegentSarva/VYOMM/internal/telemetry"
)

func device(hostname string, role telemetry.DeviceRole, cpu, mem, bw, temp, lat, loss float64) telemetry.DeviceTelemetry {
	return telemetry.DeviceTelemetry{
		Hostname:          hostname,
		Role:              role,
		CPUPercent:        cpu,
		MemoryPercent:     mem,
		BandwidthPercent:  bw,
		TemperatureC:      temp,
		LatencyMS:         lat,
		PacketLossPercent: loss,
		Status:            telemetry.StatusHealthy,
		ObservedAt:        time.Now().UTC(),
		Source:            telemetry.SourceSynthetic,
		Mode:              telemetry.ModeTrial,
	}
}

func TestCorrelate_NoMatchForHealthyDevices(t *testing.T) {
	devices := []telemetry.DeviceTelemetry{device("rtr-01", telemetry.RoleRouter, 40, 30, 20, 50, 15, 0.1)}
	got := Correlate(devices)
	if len(got) != 0 {
		t.Fatalf("expected no candidates for healthy device, got %d: %+v", len(got), got)
	}
}

func TestCorrelate_MemoryLeakMatch(t *testing.T) {
	devices := []telemetry.DeviceTelemetry{device("rtr-01", telemetry.RoleRouter, 85, 96, 20, 50, 15, 0.1)}
	got := Correlate(devices)
	if len(got) != 1 {
		t.Fatalf("expected 1 candidate, got %d", len(got))
	}
	if got[0].RootCause != "Memory Leak" {
		t.Errorf("expected Memory Leak, got %q", got[0].RootCause)
	}
	if got[0].OccurrenceKey != "Memory Leak:rtr-01" {
		t.Errorf("unexpected occurrence key: %q", got[0].OccurrenceKey)
	}
}

func TestCorrelate_AffectedDevicesDeduplicated(t *testing.T) {
	// rtr-01 with a memory leak; neighbors gw-01 appears via multiple prefix
	// scans in the original algorithm's neighbor search - verify no dupes.
	devices := []telemetry.DeviceTelemetry{
		device("rtr-01", telemetry.RoleRouter, 85, 96, 20, 50, 15, 0.1),
		device("gw-01", telemetry.RoleGateway, 40, 30, 20, 50, 15, 0.1),
	}
	got := Correlate(devices)
	if len(got) != 1 {
		t.Fatalf("expected 1 candidate, got %d", len(got))
	}
	seen := map[string]int{}
	for _, dev := range got[0].AffectedDevices {
		seen[dev]++
	}
	for dev, count := range seen {
		if count > 1 {
			t.Errorf("device %q appears %d times in affected_devices, expected no duplicates: %+v", dev, count, got[0].AffectedDevices)
		}
	}
}

func TestStore_UpsertCreatesNewActiveIncident(t *testing.T) {
	s := NewStore()
	c := Candidate{OccurrenceKey: "Memory Leak:rtr-01", RootCause: "Memory Leak", Severity: SeverityHigh, AffectedDevices: []string{"rtr-01"}, Confidence: 0.9, SLABreachMinutes: 22, RecommendedAction: "fix"}
	inc, created := s.Upsert(c, time.Now())
	if !created {
		t.Fatal("expected created=true for first occurrence")
	}
	if inc.Status != StatusActive {
		t.Errorf("expected new incident to be active, got %q", inc.Status)
	}
	if inc.OccurrenceSequence != 1 {
		t.Errorf("expected occurrence sequence 1, got %d", inc.OccurrenceSequence)
	}
}

func TestStore_UpsertDeduplicatesWhileActive(t *testing.T) {
	s := NewStore()
	c := Candidate{OccurrenceKey: "Memory Leak:rtr-01", RootCause: "Memory Leak", Severity: SeverityHigh, AffectedDevices: []string{"rtr-01"}, Confidence: 0.9, SLABreachMinutes: 22, RecommendedAction: "fix"}
	first, _ := s.Upsert(c, time.Now())
	second, created := s.Upsert(c, time.Now().Add(time.Second))
	if created {
		t.Fatal("expected created=false when re-detecting an already-active occurrence")
	}
	if second.ID != first.ID {
		t.Errorf("expected same incident ID while active, got %q vs %q", first.ID, second.ID)
	}
}

// TestStore_ResolvedIncidentCanRecur is the direct regression test for the
// defect documented in docs/migration-plan.md: the original Python
// implementation used a deterministic incident ID (hash of type+hostname)
// and its upsert_incident() returned early whenever *any* incident with
// that ID existed, regardless of status — permanently silencing that
// device+fault combination after the first resolution. This test proves
// the Go Store does not have that bug.
func TestStore_ResolvedIncidentCanRecur(t *testing.T) {
	s := NewStore()
	c := Candidate{OccurrenceKey: "CPU Saturation:rtr-01", RootCause: "CPU Saturation", Severity: SeverityHigh, AffectedDevices: []string{"rtr-01"}, Confidence: 0.9, SLABreachMinutes: 22, RecommendedAction: "fix"}

	first, created := s.Upsert(c, time.Now())
	if !created {
		t.Fatal("expected first occurrence to be created")
	}

	resolved, _, err := s.Decide(first.ID, StatusResolved, "test-user", time.Now())
	if err != nil {
		t.Fatalf("unexpected error resolving incident: %v", err)
	}
	if resolved.Status != StatusResolved {
		t.Fatalf("expected status resolved, got %q", resolved.Status)
	}

	// Re-detect the same fault on the same device after resolution.
	second, created := s.Upsert(c, time.Now().Add(time.Minute))
	if !created {
		t.Fatal("BUG: resolved incident silently suppressed a new occurrence — this is the defect being fixed")
	}
	if second.ID == first.ID {
		t.Fatalf("expected a new incident ID for the recurrence, got the same ID %q", second.ID)
	}
	if second.OccurrenceSequence != 2 {
		t.Errorf("expected occurrence sequence 2 for the recurrence, got %d", second.OccurrenceSequence)
	}
	if second.OccurrenceKey != first.OccurrenceKey {
		t.Errorf("expected recurrence to share the same occurrence key, got %q vs %q", second.OccurrenceKey, first.OccurrenceKey)
	}

	// History must preserve both occurrences in order.
	history := s.History(c.OccurrenceKey)
	if len(history) != 2 || history[0] != first.ID || history[1] != second.ID {
		t.Errorf("expected history [%q, %q], got %v", first.ID, second.ID, history)
	}

	// The first (resolved) incident must still be retrievable, not overwritten.
	all := s.List()
	statuses := map[string]Status{}
	for _, inc := range all {
		statuses[inc.ID] = inc.Status
	}
	if statuses[first.ID] != StatusResolved {
		t.Errorf("expected first incident to remain resolved in history, got %q", statuses[first.ID])
	}
	if statuses[second.ID] != StatusActive {
		t.Errorf("expected second incident to be active, got %q", statuses[second.ID])
	}
}

func TestStore_IgnoredIncidentCanAlsoRecur(t *testing.T) {
	s := NewStore()
	c := Candidate{OccurrenceKey: "Switch Overheating:sw-01", RootCause: "Switch Overheating", Severity: SeverityHigh, AffectedDevices: []string{"sw-01"}, Confidence: 0.8, SLABreachMinutes: 12, RecommendedAction: "fix"}
	first, _ := s.Upsert(c, time.Now())
	if _, _, err := s.Decide(first.ID, StatusIgnored, "user", time.Now()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	second, created := s.Upsert(c, time.Now().Add(time.Minute))
	if !created {
		t.Fatal("expected ignored incident to allow recurrence, same as resolved")
	}
	if second.OccurrenceSequence != 2 {
		t.Errorf("expected sequence 2, got %d", second.OccurrenceSequence)
	}
}

func TestStore_DecideUnknownIncidentReturnsError(t *testing.T) {
	s := NewStore()
	_, _, err := s.Decide("INC-DOES-NOT-EXIST", StatusResolved, "user", time.Now())
	if err == nil {
		t.Fatal("expected error for unknown incident ID, got nil")
	}
}

func TestStore_DecisionsAuditTrailAccumulates(t *testing.T) {
	s := NewStore()
	c := Candidate{OccurrenceKey: "k", RootCause: "x", Severity: SeverityLow, AffectedDevices: []string{"a"}, Confidence: 0.5, SLABreachMinutes: 5, RecommendedAction: "y"}
	inc, _ := s.Upsert(c, time.Now())
	s.Decide(inc.ID, StatusIgnored, "alice", time.Now())
	s.Decide(inc.ID, StatusResolved, "bob", time.Now().Add(time.Second))
	trail := s.Decisions(inc.ID)
	if len(trail) != 2 {
		t.Fatalf("expected 2 decisions in audit trail, got %d", len(trail))
	}
	if trail[0].Actor != "alice" || trail[1].Actor != "bob" {
		t.Errorf("expected ordered audit trail alice,bob, got %+v", trail)
	}
}

func TestStore_ActiveReturnsFalseWhenNoneActive(t *testing.T) {
	s := NewStore()
	_, ok := s.Active()
	if ok {
		t.Fatal("expected no active incident in empty store")
	}
}

func TestStore_ActiveReturnsMostRecent(t *testing.T) {
	s := NewStore()
	c1 := Candidate{OccurrenceKey: "k1", RootCause: "a", Severity: SeverityLow, AffectedDevices: []string{"a"}, Confidence: 0.5, SLABreachMinutes: 5, RecommendedAction: "y"}
	c2 := Candidate{OccurrenceKey: "k2", RootCause: "b", Severity: SeverityLow, AffectedDevices: []string{"b"}, Confidence: 0.5, SLABreachMinutes: 5, RecommendedAction: "y"}
	now := time.Now()
	s.Upsert(c1, now)
	second, _ := s.Upsert(c2, now.Add(time.Second))
	active, ok := s.Active()
	if !ok {
		t.Fatal("expected an active incident")
	}
	if active.ID != second.ID {
		t.Errorf("expected most recent incident to be active, got %q want %q", active.ID, second.ID)
	}
}
