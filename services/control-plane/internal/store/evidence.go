package store

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"

	"cisync.dev/cisync/control-plane/internal/domain"
)

// EvidenceRef is the minimal accepted-evidence identity used for I-03
// uniqueness checks and decision references.
type EvidenceRef struct {
	ID      string
	Kind    string
	Attempt int
}

// AcceptedEvidenceForRun returns the accepted records of one run (I-03).
func (s *Store) AcceptedEvidenceForRun(ctx context.Context, tenantID, runID string) ([]EvidenceRef, error) {
	rows, err := s.Pool.Query(ctx,
		`SELECT id, kind, attempt FROM ctrl.evidence_records
		 WHERE tenant_id=$1 AND run_id=$2 AND status='accepted'`, tenantID, runID)
	if err != nil {
		return nil, fmt.Errorf("accepted evidence: %w", err)
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

// AcceptedEvidenceRefsForCandidate returns minimal identity refs of every
// accepted record across a candidate's runs — the sufficiency input (D8).
func (s *Store) AcceptedEvidenceRefsForCandidate(ctx context.Context, tenantID, candidateID string) ([]EvidenceRef, error) {
	rows, err := s.Pool.Query(ctx,
		`SELECT id, kind, attempt FROM ctrl.evidence_records
		 WHERE tenant_id=$1 AND candidate_id=$2 AND status='accepted' AND verdict='pass'`,
		tenantID, candidateID)
	if err != nil {
		return nil, fmt.Errorf("candidate evidence: %w", err)
	}
	defer rows.Close()
	var out []EvidenceRef
	for rows.Next() {
		var ref EvidenceRef
		if err := rows.Scan(&ref.ID, &ref.Kind, &ref.Attempt); err != nil {
			return nil, fmt.Errorf("scan candidate evidence: %w", err)
		}
		out = append(out, ref)
	}
	return out, rows.Err()
}

// InsertEvidenceTx persists an accepted evidence projection row.
func InsertEvidenceTx(ctx context.Context, tx pgx.Tx, rec *domain.EvidenceRecord, seq int64) error {
	_, err := tx.Exec(ctx,
		`INSERT INTO ctrl.evidence_records (tenant_id, id, seq, run_id, attempt, candidate_id,
		   kind, verdict, status, digests, inputs_hash, confidence, cost_millicents,
		   produced_by_lease, accepted_at, created_at)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,now(),now())
		 ON CONFLICT (id) DO NOTHING`,
		rec.TenantID, rec.ID, seq, rec.RunID, rec.Attempt, rec.CandidateID,
		rec.Kind, rec.Verdict, rec.Status, toTextSlice(rec.Digests), rec.InputsHash,
		rec.Confidence, rec.CostMillicents, nullableString(rec.ProducedByLease),
	)
	if err != nil {
		return fmt.Errorf("insert evidence: %w", err)
	}
	return nil
}
