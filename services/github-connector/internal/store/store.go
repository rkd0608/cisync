// Package store persists ghconn-owned state: known installations and the
// check reports already rendered per decision (idempotency guard).
package store

import (
	"context"
	"time"

	"sauron.dev/sauron/github-connector/internal/domain"
)

// CheckReport is one persisted check publication.
type CheckReport struct {
	DecisionID  string
	CandidateID string
	Repo        string
	HeadSHA     string
	Verb        domain.DecisionVerb
	Conclusion  string
	CheckRunID  int64
	DryRun      bool
	CreatedAt   time.Time
}

// Store is the persistence surface of the connector.
type Store interface {
	// GetCheckReport returns the report for a decision, or ErrNotFound.
	GetCheckReport(ctx context.Context, decisionID string) (*CheckReport, error)
	// SaveCheckReport inserts a report; a duplicate decision id returns ErrDuplicate.
	SaveCheckReport(ctx context.Context, rep CheckReport) error
	// Close releases resources.
	Close()
}
