package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"sauron.dev/sauron/github-connector/internal/domain"
	"sauron.dev/sauron/github-connector/internal/tracking"
)

// PGTracker is the durable tracking.Store over ghconn.revision_tracking
// (migration 0004): ONE row per candidate revision walking
// queued → in_progress → completed plus the last applied decision payload
// for replay_cached re-runs. Survives connector restarts; the in-memory
// implementation remains the test wiring.
type PGTracker struct {
	st *PGStore
}

// NewTracker wires the durable revision tracker onto a connected PGStore.
func NewTracker(st *PGStore) *PGTracker { return &PGTracker{st: st} }

// RecordCheckReport implements tracking.Store with the memory store's
// field-wise upsert semantics: partial updates never blank tracked state,
// and conclusion/stalled move ONLY with an explicit completed phase.
func (p *PGTracker) RecordCheckReport(ctx context.Context, rec tracking.Record) error {
	var lastDecision []byte
	if rec.LastDecision != nil {
		raw, err := json.Marshal(rec.LastDecision)
		if err != nil {
			return fmt.Errorf("ghconn tracker: marshal decision: %w", err)
		}
		lastDecision = raw
	}
	_, err := p.st.pool.Exec(ctx, `
		INSERT INTO ghconn.revision_tracking
		  (candidate_id, head_sha, repo, check_run_id, phase, conclusion,
		   decision_id, last_decision, stalled, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,now())
		ON CONFLICT (candidate_id, head_sha) DO UPDATE SET
		  repo         = CASE WHEN EXCLUDED.repo <> ''        THEN EXCLUDED.repo        ELSE revision_tracking.repo END,
		  check_run_id = CASE WHEN EXCLUDED.check_run_id <> 0 THEN EXCLUDED.check_run_id ELSE revision_tracking.check_run_id END,
		  phase        = CASE WHEN EXCLUDED.phase <> ''       THEN EXCLUDED.phase       ELSE revision_tracking.phase END,
		  conclusion   = CASE WHEN EXCLUDED.phase = 'completed' THEN EXCLUDED.conclusion ELSE revision_tracking.conclusion END,
		  stalled      = CASE WHEN EXCLUDED.phase = 'completed' THEN EXCLUDED.stalled   ELSE revision_tracking.stalled END,
		  decision_id  = CASE WHEN EXCLUDED.decision_id <> '' THEN EXCLUDED.decision_id ELSE revision_tracking.decision_id END,
		  last_decision = COALESCE(EXCLUDED.last_decision, revision_tracking.last_decision),
		  updated_at   = now()`,
		rec.CandidateID, rec.HeadSHA, rec.Repo, rec.CheckRunID, string(rec.Phase),
		rec.Conclusion, rec.DecisionID, lastDecision, rec.Stalled)
	if err != nil {
		return fmt.Errorf("ghconn tracker: record revision %s/%s: %w", rec.CandidateID, rec.HeadSHA, err)
	}
	return nil
}

// LookupCheckReport implements tracking.Store.
func (p *PGTracker) LookupCheckReport(ctx context.Context, candidateID, headSHA string) (*tracking.Record, error) {
	row := p.st.pool.QueryRow(ctx, `
		SELECT candidate_id, head_sha, repo, check_run_id, phase, conclusion,
		       decision_id, last_decision, stalled, updated_at
		FROM ghconn.revision_tracking
		WHERE candidate_id=$1 AND head_sha=$2`, candidateID, headSHA)
	return scanRevision(row)
}

// FindByDecision implements tracking.Store: the CURRENT decision of a
// revision resolves; stale ids return ErrNotFound (decision-push dedupe).
func (p *PGTracker) FindByDecision(ctx context.Context, decisionID string) (*tracking.Record, error) {
	row := p.st.pool.QueryRow(ctx, `
		SELECT candidate_id, head_sha, repo, check_run_id, phase, conclusion,
		       decision_id, last_decision, stalled, updated_at
		FROM ghconn.revision_tracking
		WHERE decision_id=$1 AND decision_id <> ''`, decisionID)
	return scanRevision(row)
}

// OpenCheckReports implements tracking.Store: non-completed revisions not
// updated since updatedBefore, oldest first, capped at limit (sweeper feed).
func (p *PGTracker) OpenCheckReports(ctx context.Context, updatedBefore time.Time, limit int) ([]tracking.Record, error) {
	rows, err := p.st.pool.Query(ctx, `
		SELECT candidate_id, head_sha, repo, check_run_id, phase, conclusion,
		       decision_id, last_decision, stalled, updated_at
		FROM ghconn.revision_tracking
		WHERE phase <> 'completed' AND updated_at < $1
		ORDER BY updated_at ASC
		LIMIT $2`, updatedBefore, limit)
	if err != nil {
		return nil, fmt.Errorf("ghconn tracker: open revisions: %w", err)
	}
	defer rows.Close()
	var out []tracking.Record
	for rows.Next() {
		rec, err := scanRevision(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *rec)
	}
	return out, rows.Err()
}

// rowScanner abstracts pgx.Row and pgx.Rows for one shared scan path.
type rowScanner interface{ Scan(dest ...any) error }

func scanRevision(row rowScanner) (*tracking.Record, error) {
	var rec tracking.Record
	var phase string
	var lastDecision []byte
	err := row.Scan(&rec.CandidateID, &rec.HeadSHA, &rec.Repo, &rec.CheckRunID,
		&phase, &rec.Conclusion, &rec.DecisionID, &lastDecision, &rec.Stalled, &rec.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, tracking.ErrNotFound
		}
		return nil, fmt.Errorf("ghconn tracker: scan revision: %w", err)
	}
	rec.Phase = domain.CheckPhase(phase)
	if len(lastDecision) > 0 {
		decision := &domain.DecisionEnvelope{}
		if err := json.Unmarshal(lastDecision, decision); err != nil {
			return nil, fmt.Errorf("ghconn tracker: decode decision: %w", err)
		}
		rec.LastDecision = decision
	}
	return &rec, nil
}
