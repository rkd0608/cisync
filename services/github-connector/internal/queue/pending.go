// Package queue is the outbox-style pending-write buffer: when the local
// write budget is exhausted or GitHub is unavailable, required-check writes
// are QUEUED here and drained later — never silently dropped (plan §4.6).
package queue

import (
	"context"
	"strconv"
	"sync"
	"time"

	"sauron.dev/sauron/github-connector/internal/checks"
)

// Op discriminates the buffered write shape.
type Op string

// Buffered write operations.
const (
	OpCreateCheck Op = "create_check"
	OpUpdateCheck Op = "update_check"
)

// PendingWrite is one deferred GitHub check write with idempotent identity:
// Key carries the §4 idempotency basis (decision_id or candidate:phase) so a
// redelivered enqueue collapses instead of double-writing.
type PendingWrite struct {
	ID             string
	Key            string
	InstallationID int64
	Repo           string
	Op             Op
	CheckRunID     int64 // update ops only
	Payload        checks.CheckPayload
	CreatedAt      time.Time
	NextAttemptAt  time.Time
	Attempts       int
}

// Store is the durable surface for pending writes. The production
// implementation backs onto ghconn.pending_writes (migration 0003); tests
// use the memory store below.
type Store interface {
	Enqueue(ctx context.Context, w PendingWrite) error
	// Due returns up to limit writes with NextAttemptAt <= now.
	Due(ctx context.Context, now time.Time, limit int) ([]PendingWrite, error)
	MarkDelivered(ctx context.Context, id string, at time.Time) error
	Reschedule(ctx context.Context, id string, next time.Time, attempts int) error
}

// MemoryStore is the in-process Store used by tests and as the default dev
// wiring until the integrator swaps in the PG-backed implementation.
type MemoryStore struct {
	mu     sync.Mutex
	writes map[string]PendingWrite
	order  []string
	now    func() time.Time
	seq    int
}

// NewMemoryStore builds an empty MemoryStore.
func NewMemoryStore(now func() time.Time) *MemoryStore {
	if now == nil {
		now = time.Now
	}
	return &MemoryStore{writes: make(map[string]PendingWrite), now: now}
}

// Enqueue implements Store; re-enqueueing an existing Key is a no-op so
// at-least-once relays never stack duplicates.
func (m *MemoryStore) Enqueue(_ context.Context, w PendingWrite) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, dup := m.byKeyLocked(w.Key); dup {
		return nil
	}
	m.seq++
	if w.ID == "" {
		w.ID = "pw_" + m.now().UTC().Format("20060102150405") + "_" + strconv.Itoa(m.seq)
	}
	if w.CreatedAt.IsZero() {
		w.CreatedAt = m.now()
	}
	if w.NextAttemptAt.IsZero() {
		w.NextAttemptAt = w.CreatedAt
	}
	m.writes[w.ID] = w
	m.order = append(m.order, w.ID)
	return nil
}

func (m *MemoryStore) byKeyLocked(key string) (PendingWrite, bool) {
	for _, id := range m.order {
		if w := m.writes[id]; w.Key == key {
			return w, true
		}
	}
	return PendingWrite{}, false
}

// Due implements Store.
func (m *MemoryStore) Due(_ context.Context, now time.Time, limit int) ([]PendingWrite, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var due []PendingWrite
	for _, id := range m.order {
		if len(due) == limit {
			break
		}
		w := m.writes[id]
		if !w.NextAttemptAt.After(now) {
			due = append(due, w)
		}
	}
	return due, nil
}

// MarkDelivered implements Store.
func (m *MemoryStore) MarkDelivered(_ context.Context, id string, _ time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.writes, id)
	for i, existing := range m.order {
		if existing == id {
			m.order = append(m.order[:i], m.order[i+1:]...)
			break
		}
	}
	return nil
}

// Reschedule implements Store.
func (m *MemoryStore) Reschedule(_ context.Context, id string, next time.Time, attempts int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	w, ok := m.writes[id]
	if !ok {
		return nil
	}
	w.NextAttemptAt = next
	w.Attempts = attempts
	m.writes[id] = w
	return nil
}

// Depth reports the current backlog size (metrics gauge).
func (m *MemoryStore) Depth() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.writes)
}
