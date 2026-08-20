package persistence

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/GrandRegentSarva/VYOMM/internal/detection"
	"github.com/GrandRegentSarva/VYOMM/internal/incidents"
	"github.com/GrandRegentSarva/VYOMM/internal/telemetry"
)

func testDevice(hostname string, cpu float64, observedAt time.Time) telemetry.DeviceTelemetry {
	return telemetry.DeviceTelemetry{
		Hostname:          hostname,
		Role:              telemetry.RoleRouter,
		CPUPercent:        cpu,
		MemoryPercent:     30,
		BandwidthPercent:  20,
		TemperatureC:      50,
		LatencyMS:         15,
		PacketLossPercent: 0.1,
		UptimeSeconds:     100,
		Status:            telemetry.StatusHealthy,
		ObservedAt:        observedAt,
		Source:            telemetry.SourceSynthetic,
		Mode:              telemetry.ModeTrial,
	}
}

func TestNewStore_RunsMigrationsAndOpens(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.db")
	s, err := NewStore(path, 24*time.Hour, time.Now())
	if err != nil {
		t.Fatalf("unexpected error creating store: %v", err)
	}
	defer s.Close()

	var tableCount int
	row := s.DB().QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='table' AND name IN ('telemetry','incidents','incident_decisions')`)
	if err := row.Scan(&tableCount); err != nil {
		t.Fatalf("query sqlite_master: %v", err)
	}
	if tableCount != 3 {
		t.Fatalf("expected 3 migrated tables, found %d", tableCount)
	}
}

func TestIngest_RejectsInvalidRowsWithReason(t *testing.T) {
	dir := t.TempDir()
	s, err := NewStore(filepath.Join(dir, "test.db"), 24*time.Hour, time.Now())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer s.Close()

	now := time.Now().UTC()
	valid := testDevice("rtr-01", 50, now)
	invalid := testDevice("rtr-02", 150, now) // out of range

	result, err := s.Ingest([]telemetry.DeviceTelemetry{valid, invalid}, "run-1", now)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Accepted != 1 || result.Rejected != 1 {
		t.Fatalf("expected 1 accepted, 1 rejected, got %+v", result)
	}
	if len(result.Errors) != 1 {
		t.Fatalf("expected 1 recorded error, got %d: %v", len(result.Errors), result.Errors)
	}
}

// TestRestore_SurvivesRestart is the direct regression test for the
// original defect: the Python store wrote telemetry/incidents to SQLite but
// never read them back on startup, so all state vanished on restart despite
// rows existing in the database (confirmed in docs/migration-plan.md via
// direct execution of the Python AppStore). This test proves the Go store
// does not have that bug: after closing and reopening against the same
// database file, both telemetry history and incidents must still be
// present in memory.
func TestRestore_SurvivesRestart(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "restart.db")
	now := time.Now().UTC()

	s1, err := NewStore(path, 24*time.Hour, now)
	if err != nil {
		t.Fatalf("unexpected error creating first store: %v", err)
	}
	d := testDevice("rtr-01", 99, now)
	if _, err := s1.Ingest([]telemetry.DeviceTelemetry{d}, "run-1", now); err != nil {
		t.Fatalf("unexpected ingest error: %v", err)
	}
	cand := incidents.Candidate{
		OccurrenceKey: "CPU Saturation:rtr-01", RootCause: "CPU Saturation", Severity: incidents.SeverityHigh,
		AffectedDevices: []string{"rtr-01"}, Confidence: 0.9, SLABreachMinutes: 22, RecommendedAction: "fix",
	}
	inc, created, err := s1.UpsertIncident(cand, now)
	if err != nil || !created {
		t.Fatalf("unexpected error/created upserting incident: %v created=%v", err, created)
	}
	if err := s1.Close(); err != nil {
		t.Fatalf("unexpected error closing first store: %v", err)
	}

	// Simulate a process restart: brand-new Store instance, same DB file.
	s2, err := NewStore(path, 24*time.Hour, now.Add(time.Minute))
	if err != nil {
		t.Fatalf("unexpected error creating second store (restart): %v", err)
	}
	defer s2.Close()

	restoredHistory := s2.History("rtr-01")
	if len(restoredHistory) != 1 {
		t.Fatalf("BUG: telemetry history not restored after restart, got %d entries, want 1", len(restoredHistory))
	}
	if restoredHistory[0].CPUPercent != 99 {
		t.Errorf("expected restored CPU=99, got %v", restoredHistory[0].CPUPercent)
	}

	restoredIncidents := s2.ListIncidents()
	if len(restoredIncidents) != 1 {
		t.Fatalf("BUG: incidents not restored after restart, got %d, want 1", len(restoredIncidents))
	}
	if restoredIncidents[0].ID != inc.ID {
		t.Errorf("expected restored incident ID %q, got %q", inc.ID, restoredIncidents[0].ID)
	}
	if restoredIncidents[0].Status != incidents.StatusActive {
		t.Errorf("expected restored incident to be active, got %q", restoredIncidents[0].Status)
	}
}

// TestRestore_RecurrenceHistoryPreservedAcrossRestart proves that resolving
// an incident, restarting, and then re-detecting the same fault produces a
// new occurrence — combining both fixed defects (persistence restore +
// recurrence) in one end-to-end check.
func TestRestore_RecurrenceHistoryPreservedAcrossRestart(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "recur.db")
	now := time.Now().UTC()

	s1, err := NewStore(path, 24*time.Hour, now)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	cand := incidents.Candidate{
		OccurrenceKey: "CPU Saturation:rtr-01", RootCause: "CPU Saturation", Severity: incidents.SeverityHigh,
		AffectedDevices: []string{"rtr-01"}, Confidence: 0.9, SLABreachMinutes: 22, RecommendedAction: "fix",
	}
	first, _, err := s1.UpsertIncident(cand, now)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, _, err := s1.DecideIncident(first.ID, incidents.StatusResolved, "user", now); err != nil {
		t.Fatalf("unexpected error resolving: %v", err)
	}
	if err := s1.Close(); err != nil {
		t.Fatalf("unexpected error closing: %v", err)
	}

	s2, err := NewStore(path, 24*time.Hour, now.Add(time.Minute))
	if err != nil {
		t.Fatalf("unexpected error reopening: %v", err)
	}
	defer s2.Close()

	second, created, err := s2.UpsertIncident(cand, now.Add(time.Minute))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !created {
		t.Fatal("BUG: resolved incident restored from disk still suppressed recurrence after restart")
	}
	if second.ID == first.ID {
		t.Fatalf("expected new incident ID for recurrence after restart, got same ID %q", first.ID)
	}
	if second.OccurrenceSequence != 2 {
		t.Errorf("expected occurrence sequence 2, got %d", second.OccurrenceSequence)
	}

	all := s2.ListIncidents()
	if len(all) != 2 {
		t.Fatalf("expected 2 incidents total (resolved + recurrence), got %d", len(all))
	}
}

// TestPrune_RemovesRowsOlderThanRetention is the regression test for the
// original unbounded-growth defect (a 35 MB SQLite file after ~28 minutes
// of unthrottled inserts with no pruning, per docs/migration-plan.md).
func TestPrune_RemovesRowsOlderThanRetention(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "prune.db")
	now := time.Now().UTC()
	retention := 1 * time.Hour

	s, err := NewStore(path, retention, now)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer s.Close()

	old := testDevice("rtr-01", 40, now.Add(-2*time.Hour)) // outside retention
	recent := testDevice("rtr-01", 45, now.Add(-10*time.Minute))
	if _, err := s.Ingest([]telemetry.DeviceTelemetry{old, recent}, "run-1", now); err != nil {
		t.Fatalf("unexpected ingest error: %v", err)
	}

	var totalBefore int
	if err := s.DB().QueryRow(`SELECT count(*) FROM telemetry`).Scan(&totalBefore); err != nil {
		t.Fatalf("count before prune: %v", err)
	}
	if totalBefore != 2 {
		t.Fatalf("expected 2 rows before prune, got %d", totalBefore)
	}

	pruned, err := s.Prune(now)
	if err != nil {
		t.Fatalf("unexpected prune error: %v", err)
	}
	if pruned != 1 {
		t.Fatalf("expected 1 row pruned (the one outside retention), got %d", pruned)
	}

	var totalAfter int
	if err := s.DB().QueryRow(`SELECT count(*) FROM telemetry`).Scan(&totalAfter); err != nil {
		t.Fatalf("count after prune: %v", err)
	}
	if totalAfter != 1 {
		t.Fatalf("expected 1 row remaining after prune, got %d", totalAfter)
	}
}

func TestDecideIncident_UnknownIDReturnsError(t *testing.T) {
	dir := t.TempDir()
	s, err := NewStore(filepath.Join(dir, "test.db"), time.Hour, time.Now())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer s.Close()

	if _, _, err := s.DecideIncident("INC-MISSING", incidents.StatusResolved, "user", time.Now()); err == nil {
		t.Fatal("expected error for unknown incident ID")
	}
}

func TestDecisions_AuditTrailPersistsAndRestores(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.db")
	now := time.Now().UTC()

	s1, err := NewStore(path, time.Hour, now)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	cand := incidents.Candidate{OccurrenceKey: "k", RootCause: "x", Severity: incidents.SeverityLow, AffectedDevices: []string{"a"}, Confidence: 0.5, SLABreachMinutes: 5, RecommendedAction: "y"}
	inc, _, err := s1.UpsertIncident(cand, now)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, _, err := s1.DecideIncident(inc.ID, incidents.StatusIgnored, "alice", now); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := s1.Close(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	s2, err := NewStore(path, time.Hour, now.Add(time.Minute))
	if err != nil {
		t.Fatalf("unexpected error reopening: %v", err)
	}
	defer s2.Close()

	trail := s2.Decisions(inc.ID)
	if len(trail) != 1 || trail[0].Actor != "alice" {
		t.Fatalf("expected restored audit trail with alice's decision, got %+v", trail)
	}
}

func TestIngest_DetectsAnomaliesFromValidRows(t *testing.T) {
	dir := t.TempDir()
	s, err := NewStore(filepath.Join(dir, "test.db"), time.Hour, time.Now())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer s.Close()

	now := time.Now().UTC()
	critical := testDevice("rtr-01", 99, now) // triggers cpu_saturation anomaly
	result, err := s.Ingest([]telemetry.DeviceTelemetry{critical}, "run-1", now)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Detection is now a separate step from Ingest (see RecordAnomalies'
	// doc comment) so callers can trace it independently; this exercises
	// the same two-step sequence the HTTP handler uses.
	s.RecordAnomalies(detection.Detect(result.ValidDevices, now))
	anomalies := s.Anomalies()
	if len(anomalies) == 0 {
		t.Fatal("expected at least one detected anomaly for CPU=99")
	}
	found := false
	for _, a := range anomalies {
		if a.Signal == "cpu_saturation" && a.Device == "rtr-01" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected a cpu_saturation anomaly for rtr-01, got %+v", anomalies)
	}
}

func TestIngest_AnomaliesDedupeWithinDetectionWindow(t *testing.T) {
	dir := t.TempDir()
	s, err := NewStore(filepath.Join(dir, "test.db"), time.Hour, time.Now())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer s.Close()

	// Use a minute-aligned timestamp (divisible by the 20s detection bucket)
	// so the two ingests below deterministically fall in the same dedup
	// window. Using time.Now() made this test flake ~10% of the time when
	// the two calls straddled a bucket boundary.
	now := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	critical := testDevice("rtr-01", 99, now)
	result1, err := s.Ingest([]telemetry.DeviceTelemetry{critical}, "run-1", now)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	s.RecordAnomalies(detection.Detect(result1.ValidDevices, now))
	result2, err := s.Ingest([]telemetry.DeviceTelemetry{critical}, "run-1", now.Add(2*time.Second))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	s.RecordAnomalies(detection.Detect(result2.ValidDevices, now.Add(2*time.Second)))
	countCPU := 0
	for _, a := range s.Anomalies() {
		if a.Signal == "cpu_saturation" && a.Device == "rtr-01" {
			countCPU++
		}
	}
	if countCPU != 1 {
		t.Errorf("expected exactly 1 deduplicated cpu_saturation anomaly within the detection window, got %d", countCPU)
	}
}

func TestUpsertIncident_DeduplicatesWhileActive(t *testing.T) {
	dir := t.TempDir()
	s, err := NewStore(filepath.Join(dir, "test.db"), time.Hour, time.Now())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer s.Close()

	now := time.Now().UTC()
	cand := incidents.Candidate{OccurrenceKey: "k", RootCause: "x", Severity: incidents.SeverityLow, AffectedDevices: []string{"a"}, Confidence: 0.5, SLABreachMinutes: 5, RecommendedAction: "y"}
	first, created1, err := s.UpsertIncident(cand, now)
	if err != nil || !created1 {
		t.Fatalf("unexpected first upsert: err=%v created=%v", err, created1)
	}
	second, created2, err := s.UpsertIncident(cand, now.Add(time.Second))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if created2 {
		t.Fatal("expected dedup (created=false) for repeat detection while active")
	}
	if second.ID != first.ID {
		t.Errorf("expected same ID while active, got %q vs %q", first.ID, second.ID)
	}
}
