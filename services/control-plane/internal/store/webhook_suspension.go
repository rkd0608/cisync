package store

// G10 suspension + policy projection reads, split from
// webhook_projection.go to respect the charter's 250-line cap.

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// PolicyProjection is one served row of the ctrl.policies projection.
type PolicyProjection struct {
	PolicyID    string
	Version     int
	Status      string
	ActivatedAt *time.Time
	Body        json.RawMessage
}

// PolicyProjections serves GET /v1/policies/active from the ctrl.policies
// projection (readonly). history=false returns only status='active' rows.
func (s *Store) PolicyProjections(ctx context.Context, tenantID string, history bool) ([]PolicyProjection, error) {
	query := `SELECT id, version, status, activated_at, body FROM ctrl.policies WHERE tenant_id=$1`
	args := []any{tenantID}
	if !history {
		query += ` AND status='active'`
	}
	query += ` ORDER BY id, version DESC`
	rows, err := s.Pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("policy projections: %w", err)
	}
	defer rows.Close()
	out := []PolicyProjection{}
	for rows.Next() {
		var view PolicyProjection
		var body []byte
		var activated *time.Time
		if err := rows.Scan(&view.PolicyID, &view.Version, &view.Status, &activated, &body); err != nil {
			return nil, fmt.Errorf("scan policy: %w", err)
		}
		view.ActivatedAt = activated
		view.Body = json.RawMessage(body)
		out = append(out, view)
	}
	return out, rows.Err()
}

// scanIDRows collects a single text column from a Query result.
func scanIDRows(rows pgx.Rows, err error) ([]string, error) {
	if err != nil {
		return nil, fmt.Errorf("id rows: %w", err)
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan id row: %w", err)
		}
		out = append(out, id)
	}
	return out, rows.Err()
}
