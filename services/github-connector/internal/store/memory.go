package store

import (
	"context"
	"errors"
	"sort"
	"strings"
	"sync"
	"time"
)

// Sentinel store errors.
var (
	ErrNotFound  = errors.New("store: not found")
	ErrDuplicate = errors.New("store: duplicate decision id")
)

// MemoryStore is the in-process Store used by tests.
type MemoryStore struct {
	mu            sync.Mutex
	reports       map[string]CheckReport
	installations map[int64]Installation
	repos         map[string]int64 // "owner/repo" → installation id
	observations  map[string]time.Time
	obsSeq        map[string]int64
	now           func() time.Time
}

// NewMemoryStore builds an empty MemoryStore.
func NewMemoryStore(now func() time.Time) *MemoryStore {
	if now == nil {
		now = time.Now
	}
	return &MemoryStore{
		reports:       make(map[string]CheckReport),
		installations: make(map[int64]Installation),
		repos:         make(map[string]int64),
		observations:  make(map[string]time.Time),
		obsSeq:        make(map[string]int64),
		now:           now,
	}
}

// GetCheckReport implements Store.
func (m *MemoryStore) GetCheckReport(_ context.Context, decisionID string) (*CheckReport, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	rep, ok := m.reports[decisionID]
	if !ok {
		return nil, ErrNotFound
	}
	return &rep, nil
}

// SaveCheckReport implements Store.
func (m *MemoryStore) SaveCheckReport(_ context.Context, rep CheckReport) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, dup := m.reports[rep.DecisionID]; dup {
		return ErrDuplicate
	}
	rep.CreatedAt = m.now()
	m.reports[rep.DecisionID] = rep
	return nil
}

// UpsertInstallation implements Store.
func (m *MemoryStore) UpsertInstallation(_ context.Context, inst Installation) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if inst.Permissions == nil {
		inst.Permissions = map[string]string{}
	}
	m.installations[inst.ID] = inst
	return nil
}

// MarkSuspended implements Store.
func (m *MemoryStore) MarkSuspended(_ context.Context, installationID int64, suspended bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	inst, ok := m.installations[installationID]
	if !ok {
		return ErrNotFound
	}
	inst.Suspended = suspended
	m.installations[installationID] = inst
	return nil
}

// LinkRepo implements Store.
func (m *MemoryStore) LinkRepo(_ context.Context, installationID int64, owner, repo string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.installations[installationID]; !ok {
		return ErrNotFound
	}
	m.repos[owner+"/"+repo] = installationID
	return nil
}

// ResolveInstallation implements Store; unknown repos fail closed.
func (m *MemoryStore) ResolveInstallation(_ context.Context, owner, repo string) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	id, ok := m.repos[owner+"/"+repo]
	if !ok {
		return 0, ErrNotFound
	}
	return id, nil
}

// RecordCheckReport mirrors the PG semantics: retire prior live rows for the
// same revision, reject known decision ids as duplicates.
func (m *MemoryStore) RecordCheckReport(_ context.Context, rep CheckReport) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, existing := range m.reports {
		if existing.CandidateID == rep.CandidateID && existing.HeadSHA == rep.HeadSHA && id != rep.DecisionID {
			existing.Live = false
			m.reports[id] = existing
		}
	}
	if _, dup := m.reports[rep.DecisionID]; dup {
		return ErrDuplicate
	}
	rep.CreatedAt = m.now()
	rep.Live = true
	m.reports[rep.DecisionID] = rep
	key := rep.Repo
	m.obsSeq[key]++
	m.observations[key] = m.now()
	return nil
}

// InstallationStatuses implements Store.
func (m *MemoryStore) InstallationStatuses(_ context.Context, stalledAfter time.Duration, now time.Time) ([]InstallationStatus, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]InstallationStatus, 0, len(m.installations))
	for id, inst := range m.installations {
		st := InstallationStatus{
			InstallationID: id,
			Account:        inst.AccountLogin,
			Suspended:      inst.Suspended,
			Permissions:    inst.Permissions,
			Repos:          []RepoStatus{},
		}
		for full, installID := range m.repos {
			if installID != id {
				continue
			}
			name := full
			if idx := strings.IndexByte(full, '/'); idx >= 0 {
				name = full[idx+1:]
			}
			last := m.observations[full]
			var lastPtr *time.Time
			if !last.IsZero() {
				t := last
				lastPtr = &t
			}
			st.Repos = append(st.Repos, RepoStatus{
				Name:            name,
				WebhookState:    webhookState(lastPtr, stalledAfter, now),
				LastDeliverySeq: m.obsSeq[full],
				LastEventAt:     lastPtr,
			})
		}
		sort.Slice(st.Repos, func(i, j int) bool { return st.Repos[i].Name < st.Repos[j].Name })
		out = append(out, st)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].InstallationID < out[j].InstallationID })
	return out, nil
}

// Close implements Store; nothing to release.
func (m *MemoryStore) Close() {}
