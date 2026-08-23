package store

import (
	"context"
	"errors"
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
	mu      sync.Mutex
	reports map[string]CheckReport
	now     func() time.Time
}

// NewMemoryStore builds an empty MemoryStore.
func NewMemoryStore(now func() time.Time) *MemoryStore {
	if now == nil {
		now = time.Now
	}
	return &MemoryStore{reports: make(map[string]CheckReport), now: now}
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

// Close implements Store; nothing to release.
func (m *MemoryStore) Close() {}
