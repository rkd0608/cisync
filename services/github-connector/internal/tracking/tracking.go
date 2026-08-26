// Package tracking owns the connector's view of ONE check run per candidate
// revision (plan §4.1): the check_run_id walked through phases, and the last
// applied decision (needed for replay_cached re-runs). This is the
// RecordCheckReport seam — the integrator wires the PG-backed store
// implementation onto this interface.
package tracking

import (
	"context"
	"errors"
	"time"

	"cisync.dev/cisync/github-connector/internal/domain"
)

// ErrNotFound reports no tracked revision for the lookup key.
var ErrNotFound = errors.New("tracking: revision not found")

// Record is the per-(candidate_id, head_sha) check-run tracker row.
type Record struct {
	CandidateID  string
	HeadSHA      string
	Repo         string
	CheckRunID   int64 // 0 while only dry-run publications happened
	Phase        domain.CheckPhase
	Conclusion   string                   // completed rows only
	DecisionID   string                   // last applied decision
	LastDecision *domain.DecisionEnvelope // replay_cached source
	Stalled      bool
	UpdatedAt    time.Time
}

// Store persists revision tracking. Method names match the W5-A store seam
// contract so the integrator can wire them one-to-one.
type Store interface {
	// RecordCheckReport upserts the revision record keyed by
	// (candidate_id, head_sha).
	RecordCheckReport(ctx context.Context, rec Record) error
	// LookupCheckReport returns the record or ErrNotFound.
	LookupCheckReport(ctx context.Context, candidateID, headSHA string) (*Record, error)
	// FindByDecision locates a revision by its last applied decision id
	// (decision-push idempotency).
	FindByDecision(ctx context.Context, decisionID string) (*Record, error)
	// OpenCheckReports lists non-completed revisions not updated since
	// updatedBefore (stalled-sweeper feed), oldest update first, ≤ limit.
	OpenCheckReports(ctx context.Context, updatedBefore time.Time, limit int) ([]Record, error)
}
