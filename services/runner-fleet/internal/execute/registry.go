package execute

import (
	"sync"

	"cisync.dev/cisync/runner-fleet/internal/domain"
)

// Registry exposes active provider handles so the cancel endpoint can reach
// the substrate best-effort.
type Registry struct {
	mu      sync.Mutex
	handles map[string]domain.Handle
}

// NewRegistry returns an empty handle registry.
func NewRegistry() *Registry {
	return &Registry{handles: make(map[string]domain.Handle)}
}

// Register records the handle for a running run_id.
func (r *Registry) Register(runID string, h domain.Handle) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.handles[runID] = h
}

// Unregister drops the handle once the job leaves running state.
func (r *Registry) Unregister(runID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.handles, runID)
}

// Lookup returns the active handle for a run_id, if any.
func (r *Registry) Lookup(runID string) (domain.Handle, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	h, ok := r.handles[runID]
	return h, ok
}
