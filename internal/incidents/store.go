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
func (s *Store) Decide(incidentID string, status Status, actor string, now time.Time) (Incident, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	inc, ok := s.incidents[incidentID]
	if !ok {
		return Incident{}, fmt.Errorf("incident %q not found", incidentID)
	}
	inc.Status = status
	inc.UpdatedAt = now
	s.decisions[incidentID] = append(s.decisions[incidentID], Decision{
		IncidentID: incidentID,
		Status:     status,
		Actor:      actor,
		DecidedAt:  now,
	})
	return *inc, nil
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
