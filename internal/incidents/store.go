package incidents

import (
	"fmt"
	"sync"
	"time"
)

// Store holds in-memory incident state and applies recurrence rules. A
// persistence-backed implementation (internal/persistence) wraps this same
// logic with SQLite writes; this in-memory core is kept separate so the
// recurrence rules can be unit tested without a database.
type Store struct {
	mu           sync.RWMutex
	incidents    map[string]*Incident  // by ID
	byOccurrence map[string][]string   // occurrence_key -> ordered incident IDs (history)
	decisions    map[string][]Decision // incident ID -> decisions
}

func NewStore() *Store {
	return &Store{
		incidents:    make(map[string]*Incident),
		byOccurrence: make(map[string][]string),
		decisions:    make(map[string][]Decision),
	}
}

// Upsert applies one correlation candidate against current state:
//   - if an active occurrence already exists for this OccurrenceKey, it is
//     left untouched (deduplication of repeated detections of the same
//     ongoing fault) and its ID is returned with created=false;
//   - otherwise (no prior occurrence, or the most recent one was resolved
//     or ignored), a brand-new Incident is created with a fresh ID and an
//     incremented OccurrenceSequence, and created=true is returned.
//
// This is the fix for the original recurrence bug: resolving an incident
// no longer permanently silences that device+fault combination.
func (s *Store) Upsert(c Candidate, now time.Time) (incident Incident, created bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	history := s.byOccurrence[c.OccurrenceKey]
	if len(history) > 0 {
		last := s.incidents[history[len(history)-1]]
		if last != nil && last.Status == StatusActive {
			return *last, false
		}
	}

	seq := len(history) + 1
	id := NewIncidentID(c.OccurrenceKey, seq, now)
	inc := &Incident{
		ID:                        id,
		OccurrenceKey:             c.OccurrenceKey,
		OccurrenceSequence:        seq,
		Severity:                  c.Severity,
		AffectedDevices:           c.AffectedDevices,
		RootCause:                 c.RootCause,
		Status:                    StatusActive,
		CreatedAt:                 now,
		UpdatedAt:                 now,
		Confidence:                c.Confidence,
		PredictedSLABreachMinutes: c.SLABreachMinutes,
		RecommendedAction:         c.RecommendedAction,
	}
	s.incidents[id] = inc
	s.byOccurrence[c.OccurrenceKey] = append(history, id)
	return *inc, true
}

// Decide records a human decision against an incident and updates its
// status accordingly. Returns an error if the incident does not exist so
// callers can return a proper 404 rather than silently succeeding.
//
// wasActive reports whether the incident was in the active state
// immediately before this decision. Callers use it to keep the
// vyomm_incidents_active gauge accurate: the gauge must be decremented
// exactly once, when an incident leaves the active state, and never when a
// decision is applied to an already-resolved/ignored incident (which would
// otherwise drive the gauge negative).
func (s *Store) Decide(incidentID string, status Status, actor string, now time.Time) (inc Incident, wasActive bool, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	existing, ok := s.incidents[incidentID]
	if !ok {
		return Incident{}, false, fmt.Errorf("incident %q not found", incidentID)
	}
	wasActive = existing.Status == StatusActive
	existing.Status = status
	existing.UpdatedAt = now
	s.decisions[incidentID] = append(s.decisions[incidentID], Decision{
		IncidentID: incidentID,
		Status:     status,
		Actor:      actor,
		DecidedAt:  now,
	})
	return *existing, wasActive, nil
}

// Decisions returns the audit trail for one incident, oldest first.
func (s *Store) Decisions(incidentID string) []Decision {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Decision, len(s.decisions[incidentID]))
	copy(out, s.decisions[incidentID])
	return out
}

// Active returns the most recently created incident with status=active, or
// false if none exists.
func (s *Store) Active() (Incident, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var latest *Incident
	for _, inc := range s.incidents {
		if inc.Status != StatusActive {
			continue
		}
		if latest == nil || inc.CreatedAt.After(latest.CreatedAt) {
			latest = inc
		}
	}
	if latest == nil {
		return Incident{}, false
	}
	return *latest, true
}

// List returns all known incidents (all occurrences, all statuses), most
// recently created first.
func (s *Store) List() []Incident {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Incident, 0, len(s.incidents))
	for _, inc := range s.incidents {
		out = append(out, *inc)
	}
	// simple insertion sort by CreatedAt desc; incident counts are small
	// (bounded by device count * rule count), so O(n^2) is fine and keeps
	// this dependency-free.
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j].CreatedAt.After(out[j-1].CreatedAt); j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

// History returns all occurrence IDs for a given OccurrenceKey, oldest
// first, so recurrence lineage can be inspected/tested directly.
func (s *Store) History(occurrenceKey string) []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]string, len(s.byOccurrence[occurrenceKey]))
	copy(out, s.byOccurrence[occurrenceKey])
	return out
}

// Restore repopulates the in-memory Store directly from persisted rows,
// without going through Upsert/Decide (which apply recurrence rules that
// don't make sense when replaying already-decided history). This is what
// makes persistence restoration on restart possible and correct: the
// persistence layer loads rows from SQLite and calls Restore once at
// startup rather than the Store starting empty forever (the original
// Python defect this project fixes).
//
// incidentsByKey must already be ordered oldest-to-newest occurrence within
// each OccurrenceKey (the persistence layer is responsible for that
// ordering, typically via ORDER BY occurrence_sequence ASC).
func (s *Store) Restore(all []Incident, decisions map[string][]Decision) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for i := range all {
		inc := all[i]
		copyInc := inc
		s.incidents[inc.ID] = &copyInc
		s.byOccurrence[inc.OccurrenceKey] = append(s.byOccurrence[inc.OccurrenceKey], inc.ID)
	}
	for id, ds := range decisions {
		out := make([]Decision, len(ds))
		copy(out, ds)
		s.decisions[id] = out
	}
}
