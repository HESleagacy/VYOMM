// Package persistence provides SQLite-backed durable storage for telemetry
// and incidents, with goose-managed schema migrations, startup restoration
// (fixing the original defect where data was written but never read back),
// and retention-based pruning (fixing the original unbounded-growth defect).
package persistence

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/pressly/goose/v3"
	_ "modernc.org/sqlite"

	"github.com/GrandRegentSarva/VYOMM/internal/detection"
	"github.com/GrandRegentSarva/VYOMM/internal/incidents"
	"github.com/GrandRegentSarva/VYOMM/internal/telemetry"
)

// Store is the durable backing store for VYOMM's runtime state. It wraps an
// in-memory incidents.Store for fast reads and a bounded in-memory
// telemetry history cache, both of which are populated from SQLite on
// construction (Restore) rather than starting empty after every restart.
type Store struct {
	db        *sql.DB
	retention time.Duration

	mu      sync.RWMutex
	latest  map[string]telemetry.DeviceTelemetry   // hostname -> most recent sample
	history map[string][]telemetry.DeviceTelemetry // hostname -> ordered samples (oldest first), bounded

	incidents *incidents.Store

	anomalies []detection.Anomaly // bounded ring buffer, most recent first
}

// maxHistoryPerDevice bounds the in-memory per-device history kept for
// forecasting, independent of the SQLite retention window, so memory use
// stays flat regardless of retention hours configured.
const maxHistoryPerDevice = 90

// maxAnomalies bounds the in-memory anomaly buffer. Anomalies are
// intentionally NOT persisted to SQLite: they are cheaply re-derivable
// from telemetry (internal/detection.Detect is a pure function of recent
// telemetry), so losing them across a restart is a deliberate, documented
// design choice, not a repeat of the "written but never restored" defect —
// nothing about them is ever written and silently failed to be read back.
const maxAnomalies = 100

// Open opens (creating if necessary) the SQLite database at path and runs
// all pending goose migrations. The returned *sql.DB is otherwise unused by
// callers other than NewStore; it is exported via Store.DB() for tests that
// need to inspect raw rows.
func Open(path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("persistence: open sqlite at %q: %w", path, err)
	}
	// SQLite via modernc.org/sqlite does not support concurrent writers;
	// serialize access at the Go level to avoid "database is locked" errors
	// under concurrent ingest, which matters once the HTTP API and any
	// background pruning goroutine share this connection.
	db.SetMaxOpenConns(1)

	goose.SetBaseFS(Migrations)
	goose.SetLogger(goose.NopLogger()) // structured logging happens at the caller; keep goose quiet
	if err := goose.SetDialect("sqlite3"); err != nil {
		db.Close()
		return nil, fmt.Errorf("persistence: set goose dialect: %w", err)
	}
	if err := goose.Up(db, "migrations"); err != nil {
		db.Close()
		return nil, fmt.Errorf("persistence: run migrations: %w", err)
	}
	return db, nil
}

// NewStore opens the database, runs migrations, and restores recent state
// from it. now is passed explicitly so restoration/pruning is testable
// without relying on wall-clock time.
func NewStore(path string, retention time.Duration, now time.Time) (*Store, error) {
	db, err := Open(path)
	if err != nil {
		return nil, err
	}
	s := &Store{
		db:        db,
		retention: retention,
		latest:    make(map[string]telemetry.DeviceTelemetry),
		history:   make(map[string][]telemetry.DeviceTelemetry),
		incidents: incidents.NewStore(),
	}
	if err := s.restore(now); err != nil {
		db.Close()
		return nil, fmt.Errorf("persistence: restore state: %w", err)
	}
	return s, nil
}

// Close closes the underlying database connection.
func (s *Store) Close() error {
	return s.db.Close()
}

// DB exposes the raw connection for tests that need to assert on stored
// rows directly (e.g. retention pruning tests).
func (s *Store) DB() *sql.DB {
	return s.db
}

// restore loads telemetry within the retention window and all incidents +
// decisions from SQLite into the in-memory caches. This is what makes
// "SQLite event storage" actually mean something across restarts, unlike
// the original Python implementation which wrote rows but never read them
// back on startup.
func (s *Store) restore(now time.Time) error {
	cutoff := now.Add(-s.retention).UTC().Format(time.RFC3339)
	rows, err := s.db.Query(
		`SELECT hostname, payload FROM telemetry WHERE observed_at >= ? ORDER BY observed_at ASC`,
		cutoff,
	)
	if err != nil {
		return fmt.Errorf("query telemetry for restore: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var hostname, payload string
		if err := rows.Scan(&hostname, &payload); err != nil {
			return fmt.Errorf("scan telemetry row: %w", err)
		}
		var d telemetry.DeviceTelemetry
		if err := json.Unmarshal([]byte(payload), &d); err != nil {
			return fmt.Errorf("unmarshal telemetry payload for %q: %w", hostname, err)
		}
		s.appendHistoryLocked(d)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate telemetry rows: %w", err)
	}

	incRows, err := s.db.Query(
		`SELECT id, occurrence_key, occurrence_sequence, severity, affected_devices, root_cause,
		        status, created_at, updated_at, confidence, predicted_sla_breach_minutes, recommended_action
		 FROM incidents ORDER BY occurrence_key ASC, occurrence_sequence ASC`,
	)
	if err != nil {
		return fmt.Errorf("query incidents for restore: %w", err)
	}
	defer incRows.Close()

	var restored []incidents.Incident
	for incRows.Next() {
		var (
			inc          incidents.Incident
			affectedJSON string
			createdAt    string
			updatedAt    string
		)
		if err := incRows.Scan(&inc.ID, &inc.OccurrenceKey, &inc.OccurrenceSequence, &inc.Severity,
			&affectedJSON, &inc.RootCause, &inc.Status, &createdAt, &updatedAt, &inc.Confidence,
			&inc.PredictedSLABreachMinutes, &inc.RecommendedAction); err != nil {
			return fmt.Errorf("scan incident row: %w", err)
		}
		if err := json.Unmarshal([]byte(affectedJSON), &inc.AffectedDevices); err != nil {
			return fmt.Errorf("unmarshal affected_devices for %q: %w", inc.ID, err)
		}
		inc.CreatedAt, err = time.Parse(time.RFC3339, createdAt)
		if err != nil {
			return fmt.Errorf("parse created_at for %q: %w", inc.ID, err)
		}
		inc.UpdatedAt, err = time.Parse(time.RFC3339, updatedAt)
		if err != nil {
			return fmt.Errorf("parse updated_at for %q: %w", inc.ID, err)
		}
		restored = append(restored, inc)
	}
	if err := incRows.Err(); err != nil {
		return fmt.Errorf("iterate incident rows: %w", err)
	}

	decRows, err := s.db.Query(`SELECT incident_id, status, actor, decided_at FROM incident_decisions ORDER BY decided_at ASC`)
	if err != nil {
		return fmt.Errorf("query decisions for restore: %w", err)
	}
	defer decRows.Close()

	decisions := make(map[string][]incidents.Decision)
	for decRows.Next() {
		var d incidents.Decision
		var decidedAt string
		if err := decRows.Scan(&d.IncidentID, &d.Status, &d.Actor, &decidedAt); err != nil {
			return fmt.Errorf("scan decision row: %w", err)
		}
		d.DecidedAt, err = time.Parse(time.RFC3339, decidedAt)
		if err != nil {
			return fmt.Errorf("parse decided_at: %w", err)
		}
		decisions[d.IncidentID] = append(decisions[d.IncidentID], d)
	}
	if err := decRows.Err(); err != nil {
		return fmt.Errorf("iterate decision rows: %w", err)
	}

	s.incidents.Restore(restored, decisions)
	return nil
}

func (s *Store) appendHistoryLocked(d telemetry.DeviceTelemetry) {
	s.latest[d.Hostname] = d
	h := append(s.history[d.Hostname], d)
	if len(h) > maxHistoryPerDevice {
		h = h[len(h)-maxHistoryPerDevice:]
	}
	s.history[d.Hostname] = h
}

// IngestResult reports how many devices were accepted/rejected and why, so
// ingestion never silently drops invalid rows without a reason (per
// API_CONTRACT.md's POST /api/v1/telemetry contract). ValidDevices is
// exposed so callers (the HTTP handler) can run anomaly detection as a
// separate, independently-traceable step without re-validating.
type IngestResult struct {
	Accepted     int
	Rejected     int
	Errors       []string
	ValidDevices []telemetry.DeviceTelemetry
}

// Ingest validates and stores a batch of device telemetry. Invalid rows are
// rejected with a recorded reason rather than silently dropped or silently
// stored malformed.
func (s *Store) Ingest(devices []telemetry.DeviceTelemetry, runID string, now time.Time) (IngestResult, error) {
	var result IngestResult
	valid := make([]telemetry.DeviceTelemetry, 0, len(devices))

	for _, d := range devices {
		if err := d.Validate(); err != nil {
			result.Rejected++
			result.Errors = append(result.Errors, fmt.Sprintf("%s: %v", d.Hostname, err))
			continue
		}
		valid = append(valid, d)
		result.Accepted++
	}

	if len(valid) > 0 {
		tx, err := s.db.Begin()
		if err != nil {
			return result, fmt.Errorf("persistence: begin ingest tx: %w", err)
		}
		stmt, err := tx.Prepare(`INSERT INTO telemetry(hostname, role, payload, observed_at, run_id, created_at) VALUES (?, ?, ?, ?, ?, ?)`)
		if err != nil {
			tx.Rollback()
			return result, fmt.Errorf("persistence: prepare insert: %w", err)
		}
		createdAt := now.UTC().Format(time.RFC3339)
		for _, d := range valid {
			payload, err := json.Marshal(d)
			if err != nil {
				stmt.Close()
				tx.Rollback()
				return result, fmt.Errorf("persistence: marshal telemetry for %q: %w", d.Hostname, err)
			}
			observed := d.ObservedAt.UTC().Format(time.RFC3339)
			if _, err := stmt.Exec(d.Hostname, string(d.Role), string(payload), observed, runID, createdAt); err != nil {
				stmt.Close()
				tx.Rollback()
				return result, fmt.Errorf("persistence: insert telemetry row for %q: %w", d.Hostname, err)
			}
		}
		stmt.Close()
		if err := tx.Commit(); err != nil {
			return result, fmt.Errorf("persistence: commit ingest tx: %w", err)
		}
	}

	s.mu.Lock()
	for _, d := range valid {
		s.appendHistoryLocked(d)
	}
	s.mu.Unlock()

	result.ValidDevices = valid
	return result, nil
}

// RecordAnomalies appends already-detected anomalies to the bounded buffer.
// Detection itself (internal/detection.Detect) is deliberately NOT called
// from inside Ingest: the HTTP layer runs it as a separate step (using
// IngestResult.ValidDevices) so it can be wrapped in its own tracing span
// ("anomaly.detected"), matching the bounded seven-step workflow instead of
// bundling ingestion and detection into one opaque operation.
func (s *Store) RecordAnomalies(newAnomalies []detection.Anomaly) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.addAnomaliesLocked(newAnomalies)
}

// addAnomaliesLocked appends newly detected anomalies, deduplicating by ID
// (detection.Detect already time-buckets IDs so repeated detections within
// the same window naturally dedupe here) and enforcing maxAnomalies.
// Caller must hold s.mu.
func (s *Store) addAnomaliesLocked(newAnomalies []detection.Anomaly) {
	existing := make(map[string]bool, len(s.anomalies))
	for _, a := range s.anomalies {
		existing[a.ID] = true
	}
	for _, a := range newAnomalies {
		if existing[a.ID] {
			continue
		}
		s.anomalies = append([]detection.Anomaly{a}, s.anomalies...) // most recent first
		existing[a.ID] = true
	}
	if len(s.anomalies) > maxAnomalies {
		s.anomalies = s.anomalies[:maxAnomalies]
	}
}

// Anomalies returns the current bounded anomaly buffer, most recent first.
func (s *Store) Anomalies() []detection.Anomaly {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]detection.Anomaly, len(s.anomalies))
	copy(out, s.anomalies)
	return out
}

// LatestDevices returns the most recent known sample per device.
func (s *Store) LatestDevices() []telemetry.DeviceTelemetry {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]telemetry.DeviceTelemetry, 0, len(s.latest))
	for _, d := range s.latest {
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Hostname < out[j].Hostname })
	return out
}

// History returns the retained samples for one device, oldest first.
func (s *Store) History(hostname string) []telemetry.DeviceTelemetry {
	s.mu.RLock()
	defer s.mu.RUnlock()
	h := s.history[hostname]
	out := make([]telemetry.DeviceTelemetry, len(h))
	copy(out, h)
	return out
}

// Prune deletes telemetry rows older than the configured retention window
// and returns how many rows were removed. This fixes the original
// unbounded-growth defect (a 35 MB SQLite file after ~28 minutes of
// unthrottled inserts, per docs/migration-plan.md).
func (s *Store) Prune(now time.Time) (int64, error) {
	cutoff := now.Add(-s.retention).UTC().Format(time.RFC3339)
	res, err := s.db.Exec(`DELETE FROM telemetry WHERE observed_at < ?`, cutoff)
	if err != nil {
		return 0, fmt.Errorf("persistence: prune telemetry: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("persistence: rows affected after prune: %w", err)
	}
	return n, nil
}

// UpsertIncident applies a correlation candidate through the recurrence-safe
// incidents.Store and persists the result to SQLite.
func (s *Store) UpsertIncident(c incidents.Candidate, now time.Time) (incidents.Incident, bool, error) {
	inc, created := s.incidents.Upsert(c, now)
	if !created {
		return inc, false, nil
	}
	affectedJSON, err := json.Marshal(inc.AffectedDevices)
	if err != nil {
		return inc, true, fmt.Errorf("persistence: marshal affected_devices: %w", err)
	}
	_, err = s.db.Exec(
		`INSERT INTO incidents(id, occurrence_key, occurrence_sequence, severity, affected_devices, root_cause,
		                       status, created_at, updated_at, confidence, predicted_sla_breach_minutes, recommended_action)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		inc.ID, inc.OccurrenceKey, inc.OccurrenceSequence, inc.Severity, string(affectedJSON), inc.RootCause,
		inc.Status, inc.CreatedAt.UTC().Format(time.RFC3339), inc.UpdatedAt.UTC().Format(time.RFC3339),
		inc.Confidence, inc.PredictedSLABreachMinutes, inc.RecommendedAction,
	)
	if err != nil {
		return inc, true, fmt.Errorf("persistence: insert incident %q: %w", inc.ID, err)
	}
	return inc, true, nil
}

// DecideIncident records a decision through incidents.Store and persists
// both the updated incident status and the audit-trail row.
func (s *Store) DecideIncident(incidentID string, status incidents.Status, actor string, now time.Time) (incidents.Incident, error) {
	inc, err := s.incidents.Decide(incidentID, status, actor, now)
	if err != nil {
		return incidents.Incident{}, err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return inc, fmt.Errorf("persistence: begin decide tx: %w", err)
	}
	if _, err := tx.Exec(`UPDATE incidents SET status = ?, updated_at = ? WHERE id = ?`,
		inc.Status, inc.UpdatedAt.UTC().Format(time.RFC3339), inc.ID); err != nil {
		tx.Rollback()
		return inc, fmt.Errorf("persistence: update incident status: %w", err)
	}
	if _, err := tx.Exec(`INSERT INTO incident_decisions(incident_id, status, actor, decided_at) VALUES (?, ?, ?, ?)`,
		inc.ID, status, actor, now.UTC().Format(time.RFC3339)); err != nil {
		tx.Rollback()
		return inc, fmt.Errorf("persistence: insert decision row: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return inc, fmt.Errorf("persistence: commit decide tx: %w", err)
	}
	return inc, nil
}

// ActiveIncident, ListIncidents, and Decisions pass through to the
// in-memory incidents.Store, which is kept consistent with SQLite by
// UpsertIncident/DecideIncident above.
func (s *Store) ActiveIncident() (incidents.Incident, bool) { return s.incidents.Active() }
func (s *Store) ListIncidents() []incidents.Incident        { return s.incidents.List() }
func (s *Store) Decisions(incidentID string) []incidents.Decision {
	return s.incidents.Decisions(incidentID)
}
