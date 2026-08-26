// Package store persists ghconn-owned state: known installations and the
// check reports already rendered per decision (idempotency guard).
package store

import (
	"context"
	"time"

	"cisync.dev/cisync/github-connector/internal/domain"
)

// CheckReport is one persisted check publication. Live marks the row that
// currently represents the candidate revision's check run (≤1 live per
// (candidate_id, head_sha), plan §4.1 update-in-place).
type CheckReport struct {
	DecisionID  string
	CandidateID string
	Repo        string
	HeadSHA     string
	Verb        domain.DecisionVerb
	Conclusion  string
	CheckRunID  int64
	DryRun      bool
	Live        bool
	CreatedAt   time.Time
}

// Installation is one GitHub App installation binding (plan §5.6).
type Installation struct {
	ID           int64
	AccountLogin string
	Suspended    bool
	Permissions  map[string]string
}

// RepoStatus is the per-repo view served by GET /v1/installations/status.
type RepoStatus struct {
	Name            string
	WebhookState    string // receiving | stalled | pending
	LastDeliverySeq int64
	LastEventAt     *time.Time
}

// InstallationStatus is the per-installation status projection.
type InstallationStatus struct {
	InstallationID int64
	Account        string
	Suspended      bool
	Permissions    map[string]string
	Repos          []RepoStatus
}

// Store is the persistence surface of the connector.
type Store interface {
	// GetCheckReport returns the report for a decision, or ErrNotFound.
	GetCheckReport(ctx context.Context, decisionID string) (*CheckReport, error)
	// SaveCheckReport inserts a report; a duplicate decision id returns ErrDuplicate.
	SaveCheckReport(ctx context.Context, rep CheckReport) error
	// UpsertInstallation inserts or refreshes an installation binding
	// (installation.created / new_permissions_accepted).
	UpsertInstallation(ctx context.Context, inst Installation) error
	// MarkSuspended flips the suspension flag (installation.deleted ⇒ §6.4).
	MarkSuspended(ctx context.Context, installationID int64, suspended bool) error
	// LinkRepo caches one repo→installation edge (fail-closed resolution feed).
	LinkRepo(ctx context.Context, installationID int64, owner, repo string) error
	// ResolveInstallation maps (owner, repo) → installation id; ErrNotFound
	// means UNKNOWN repo — callers must fail closed, never guess (§6.3).
	ResolveInstallation(ctx context.Context, owner, repo string) (int64, error)
	// RecordCheckReport upserts the LIVE report for a candidate revision:
	// replays of a known decision return ErrDuplicate; a new decision for the
	// same revision supersedes the prior live row (plan §4.1 update-in-place).
	RecordCheckReport(ctx context.Context, rep CheckReport) error
	// InstallationStatuses renders the status projection read-only.
	InstallationStatuses(ctx context.Context, stalledAfter time.Duration, now time.Time) ([]InstallationStatus, error)
	// Close releases resources.
	Close()
}
