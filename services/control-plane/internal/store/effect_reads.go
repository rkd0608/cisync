package store

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"

	"cisync.dev/cisync/control-plane/internal/domain"
)

// P1-5 (W4 audit): maybeRenderEligible's four advisory reads used to run on
// the pool WHILE the caller's effect tx held pg_advisory_xact_lock — under
// burst load those cross-connection reads could starve on the very lock the
// tx held, stalling decisions. These Tx variants run the identical queries
// on the CALLER'S transaction connection: no s.Pool read ever happens while
// the append lock is held.

// LatestDecisionForCandidateTx is the tx-scoped form of
// LatestDecisionForCandidate.
func LatestDecisionForCandidateTx(ctx context.Context, q pgxQuerier, tenantID, candidateID string) (*DecisionRow, error) {
	row := q.QueryRow(ctx,
		`SELECT id, subject_id, verb, confidence, policy, explanation->>'summary', rendered_at
		 FROM ctrl.decisions WHERE tenant_id=$1 AND subject_type='candidate' AND subject_id=$2
		 ORDER BY rendered_at DESC LIMIT 1`, tenantID, candidateID)
	var d DecisionRow
	var policyRaw []byte
	err := row.Scan(&d.ID, &d.SubjectID, &d.Verb, &d.Confidence, &policyRaw, &d.Summary, &d.RenderedAt)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("latest decision: %w", err)
	}
	if err := jsonUnmarshal(policyRaw, &d.Policy); err != nil {
		return nil, fmt.Errorf("unmarshal decision policy: %w", err)
	}
	return &d, nil
}

// CountFailedRequiredRunsTx is the tx-scoped form of CountFailedRequiredRuns.
func CountFailedRequiredRunsTx(ctx context.Context, q pgxQuerier, tenantID, candidateID string, requiredKinds []string) (int, error) {
	if len(requiredKinds) == 0 {
		return 0, nil
	}
	rows, err := q.Query(ctx,
		`SELECT count(*) FROM ctrl.validation_runs
		 WHERE tenant_id=$1 AND candidate_id=$2 AND state IN ('failed','timed_out') AND job_spec->>'kind' = ANY($3)`,
		tenantID, candidateID, requiredKinds)
	if err != nil {
		return 0, fmt.Errorf("count failed required runs: %w", err)
	}
	defer rows.Close()
	var n int
	for rows.Next() {
		if err := rows.Scan(&n); err != nil {
			return 0, err
		}
	}
	return n, rows.Err()
}

// CandidateStateByIDTx is the tx-scoped form of CandidateStateByID.
func CandidateStateByIDTx(ctx context.Context, q pgxQuerier, tenantID, candidateID string) (domain.CandidateState, error) {
	var state string
	err := q.QueryRow(ctx,
		`SELECT state FROM ctrl.candidates WHERE id=$1 AND tenant_id=$2`, candidateID, tenantID).Scan(&state)
	if err != nil {
		if err == pgx.ErrNoRows {
			return "", domain.ErrNotFound
		}
		return "", fmt.Errorf("candidate state: %w", err)
	}
	return domain.CandidateState(state), nil
}

// AcceptedEvidenceRefsForCandidateTx is the tx-scoped form of
// AcceptedEvidenceRefsForCandidate.
func AcceptedEvidenceRefsForCandidateTx(ctx context.Context, q pgxQuerier, tenantID, candidateID string) ([]EvidenceRef, error) {
	rows, err := q.Query(ctx,
		`SELECT id, kind, attempt FROM ctrl.evidence_records
		 WHERE tenant_id=$1 AND candidate_id=$2 AND status='accepted' AND verdict='pass'`,
		tenantID, candidateID)
	if err != nil {
		return nil, fmt.Errorf("accepted evidence refs: %w", err)
	}
	defer rows.Close()
	var out []EvidenceRef
	for rows.Next() {
		var ref EvidenceRef
		if err := rows.Scan(&ref.ID, &ref.Kind, &ref.Attempt); err != nil {
			return nil, fmt.Errorf("scan evidence ref: %w", err)
		}
		out = append(out, ref)
	}
	return out, rows.Err()
}

// pgxQuerier is the common Query/QueryRow surface of pgx.Tx and
// pgxpool.Pool — the Queryer seam the audit asked for.
type pgxQuerier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}
