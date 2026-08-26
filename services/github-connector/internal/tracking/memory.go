package tracking

import (
	"context"
	"sort"
	"sync"
	"time"

	"cisync.dev/cisync/github-connector/internal/domain"
)

// MemoryStore is the in-process Store used by tests and as the default dev
// wiring until the integrator swaps in the PG-backed RecordCheckReport
// implementation (W5-A seam).
type MemoryStore struct {
	mu         sync.Mutex
	revisions  map[string]Record // key: candidateID + "\x00" + headSHA
	byDecision map[string]string // decision_id -> revision key
	now        func() time.Time
}

// NewMemoryStore builds an empty store.
func NewMemoryStore(now func() time.Time) *MemoryStore {
	if now == nil {
		now = time.Now
	}
	return &MemoryStore{
		revisions:  make(map[string]Record),
		byDecision: make(map[string]string),
		now:        now,
	}
}

func revisionKey(candidateID, headSHA string) string {
	return candidateID + "\x00" + headSHA
}

// RecordCheckReport implements Store (upsert per candidate revision).
func (m *MemoryStore) RecordCheckReport(_ context.Context, rec Record) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := revisionKey(rec.CandidateID, rec.HeadSHA)
	existing, ok := m.revisions[key]
	if !ok {
		existing = rec
	} else {
		// WHY field-wise merge: partial updates (e.g. a phase flip) must not
		// blank the last decision or check_run_id already tracked.
		if rec.CheckRunID != 0 {
			existing.CheckRunID = rec.CheckRunID
		}
		if rec.Phase != "" {
			existing.Phase = rec.Phase
			if rec.Phase == domain.PhaseCompleted {
				existing.Conclusion = rec.Conclusion
				existing.Stalled = rec.Stalled
			}
		}
		if rec.DecisionID != "" {
			existing.DecisionID = rec.DecisionID
		}
		if rec.LastDecision != nil {
			existing.LastDecision = rec.LastDecision
		}
		if rec.Repo != "" {
			existing.Repo = rec.Repo
		}
	}
	if existing.DecisionID != "" && existing.DecisionID != rec.DecisionID {
		delete(m.byDecision, existing.DecisionID)
	}
	existing.UpdatedAt = m.now()
	m.revisions[key] = existing
	if existing.DecisionID != "" {
		m.byDecision[existing.DecisionID] = key
	}
	return nil
}

// LookupCheckReport implements Store.
func (m *MemoryStore) LookupCheckReport(_ context.Context, candidateID, headSHA string) (*Record, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	rec, ok := m.revisions[revisionKey(candidateID, headSHA)]
	if !ok {
		return nil, ErrNotFound
	}
	return &rec, nil
}

// FindByDecision implements Store.
func (m *MemoryStore) FindByDecision(_ context.Context, decisionID string) (*Record, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	key, ok := m.byDecision[decisionID]
	if !ok {
		return nil, ErrNotFound
	}
	rec := m.revisions[key]
	return &rec, nil
}

// OpenCheckReports implements Store: non-completed revisions older than the
// threshold, oldest-first.
func (m *MemoryStore) OpenCheckReports(_ context.Context, updatedBefore time.Time, limit int) ([]Record, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var open []Record
	for _, rec := range m.revisions {
		if rec.Phase != domain.PhaseCompleted && rec.UpdatedAt.Before(updatedBefore) {
			open = append(open, rec)
		}
	}
	sort.Slice(open, func(i, j int) bool { return open[i].UpdatedAt.Before(open[j].UpdatedAt) })
	if len(open) > limit {
		open = open[:limit]
	}
	return open, nil
}
