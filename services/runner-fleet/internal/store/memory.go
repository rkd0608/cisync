package store

import (
	"context"
	"fmt"
	"sync"
	"time"

	"cisync.dev/cisync/runner-fleet/internal/domain"
)

// MemoryStore is an in-process Store mirroring the fencing, uniqueness, and
// staleness semantics of the Postgres implementation. All operations are
// serialized by a mutex, which makes claims atomic by construction.
type MemoryStore struct {
	mu      sync.Mutex
	nowFn   func() time.Time
	jobs    map[string]*FleetJob // key: run_id
	order   []string             // insertion order of run_ids for deterministic claim
	workers map[string]time.Time
	nextID  int
}

// NewMemoryStore returns an empty store using the supplied clock.
func NewMemoryStore(nowFn func() time.Time) *MemoryStore {
	if nowFn == nil {
		nowFn = time.Now
	}
	return &MemoryStore{
		nowFn:   nowFn,
		jobs:    make(map[string]*FleetJob),
		workers: make(map[string]time.Time),
	}
}

// Enqueue implements Store.
func (m *MemoryStore) Enqueue(_ context.Context, job domain.Job) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.jobs[job.RunID]; ok {
		return fmt.Errorf("memory store: enqueue: %w: %s", domain.ErrDuplicateRun, job.RunID)
	}
	m.nextID++
	m.jobs[job.RunID] = &FleetJob{
		ID:         fmt.Sprintf("job_%08d", m.nextID),
		RunID:      job.RunID,
		Attempt:    job.Attempt,
		Tier:       job.Tier,
		Pool:       job.Pool,
		Status:     domain.StatusQueued,
		FenceToken: 0,
		Spec:       job.Spec,
		LeaseToken: job.LeaseToken,
		CreatedAt:  m.nowFn(),
	}
	m.order = append(m.order, job.RunID)
	return nil
}

// ClaimJobs implements Store.
// EnsureWorker implements Store; in-memory workers are just timestamps.
func (m *MemoryStore) EnsureWorker(_ context.Context, id string, _ string, _ int, now time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.workers[id] = now
	return nil
}

func (m *MemoryStore) ClaimJobs(_ context.Context, c Claim, now time.Time) ([]domain.Job, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.workers[c.WorkerID] = now
	var claimed []domain.Job
	for _, runID := range m.order {
		if len(claimed) >= c.Limit {
			break
		}
		j := m.jobs[runID]
		if j.Pool != c.Pool || j.Status != domain.StatusQueued {
			continue
		}
		j.Status = domain.StatusRunning
		j.FenceToken++
		j.ClaimedBy = c.WorkerID
		j.ClaimedAt = now
		j.LastHeartbeat = now
		j.FinishedAt = time.Time{}
		j.Accepted = false
		j.ResultRef = nil
		claimed = append(claimed, domain.Job{
			RunID:      j.RunID,
			Attempt:    j.Attempt,
			FenceToken: j.FenceToken,
			Tier:       j.Tier,
			Pool:       j.Pool,
			Spec:       j.Spec,
			LeaseToken: j.LeaseToken,
		})
	}
	return claimed, nil
}

// Get implements Store.
func (m *MemoryStore) Get(_ context.Context, runID string) (FleetJob, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	j, ok := m.jobs[runID]
	if !ok {
		return FleetJob{}, fmt.Errorf("memory store: get %s: %w", runID, domain.ErrNotFound)
	}
	return *j, nil
}

// Heartbeat implements Store.
func (m *MemoryStore) Heartbeat(_ context.Context, runID string, fenceToken int64, now time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	j, ok := m.jobs[runID]
	if !ok {
		return fmt.Errorf("memory store: heartbeat: %w", domain.ErrNotFound)
	}
	if j.Status != domain.StatusRunning || j.FenceToken != fenceToken {
		return fmt.Errorf("memory store: heartbeat %s: %w", runID, domain.ErrFenceMismatch)
	}
	j.LastHeartbeat = now
	if j.ClaimedBy != "" {
		m.workers[j.ClaimedBy] = now
	}
	return nil
}

// Complete implements Store.
func (m *MemoryStore) Complete(_ context.Context, runID string, c Completion, now time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	j, ok := m.jobs[runID]
	if !ok {
		return fmt.Errorf("memory store: complete: %w", domain.ErrNotFound)
	}
	if j.Accepted {
		return fmt.Errorf("memory store: complete %s: %w", runID, domain.ErrAlreadyAccepted)
	}
	if j.Status != domain.StatusRunning || j.FenceToken != c.FenceToken {
		return fmt.Errorf("memory store: complete %s: %w", runID, domain.ErrFenceMismatch)
	}
	j.Status = c.Status
	j.Accepted = true
	j.FinishedAt = now
	j.ResultRef = c.ResultRef()
	return nil
}

// Cancel implements Store.
func (m *MemoryStore) Cancel(_ context.Context, runID string, reason string, now time.Time) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	j, ok := m.jobs[runID]
	if !ok {
		return false, fmt.Errorf("memory store: cancel: %w", domain.ErrNotFound)
	}
	if domain.Terminal(j.Status) {
		return false, nil
	}
	j.Status = domain.StatusCancelled
	j.FenceToken++
	j.FinishedAt = now
	j.ResultRef = map[string]any{"reason": reason, "cancelled_at": now.Format(time.RFC3339)}
	return true, nil
}

// RecordArtifacts implements Store.
func (m *MemoryStore) RecordArtifacts(_ context.Context, runID string, artifacts []domain.Artifact, now time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.jobs[runID]; !ok {
		return fmt.Errorf("memory store: record artifacts: %w", domain.ErrNotFound)
	}
	return nil
}

// RequeueStale implements Store.
func (m *MemoryStore) RequeueStale(_ context.Context, threshold time.Duration, now time.Time) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var requeued []string
	for _, runID := range m.order {
		j := m.jobs[runID]
		if j.Status != domain.StatusRunning {
			continue
		}
		if now.Sub(j.LastHeartbeat) < threshold {
			continue
		}
		j.Status = domain.StatusQueued
		j.FenceToken++
		j.ClaimedBy = ""
		requeued = append(requeued, runID)
	}
	for id, hb := range m.workers {
		if now.Sub(hb) >= threshold {
			delete(m.workers, id)
		}
	}
	return requeued, nil
}

// QueueDepth implements Store.
func (m *MemoryStore) QueueDepth(_ context.Context) (map[string]int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	depth := make(map[string]int64)
	for _, runID := range m.order {
		j := m.jobs[runID]
		if j.Status == domain.StatusQueued {
			key := fmt.Sprintf("%s/%d", j.Pool, j.Tier)
			depth[key]++
		}
	}
	return depth, nil
}

// TerminalAccepted implements Store.
func (m *MemoryStore) TerminalAccepted(_ context.Context, limit int) ([]FleetJob, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []FleetJob
	// Newest first by finished_at; insertion order is deterministic enough
	// for the memory store, so walk the order in reverse.
	for i := len(m.order) - 1; i >= 0 && len(out) < limit; i-- {
		j := m.jobs[m.order[i]]
		if j.Accepted && domain.Terminal(j.Status) {
			out = append(out, *j)
		}
	}
	return out, nil
}
