package store

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5"

	"sauron.dev/sauron/control-plane/internal/domain"
)

// InsertDecisionTx persists the decisions projection row for a rendered
// decision (immutable fact; I-09 stamp required).
func InsertDecisionTx(ctx context.Context, tx pgx.Tx, dec *domain.Decision, seq int64) error {
	explanation, err := json.Marshal(dec.Explanation)
	if err != nil {
		return fmt.Errorf("marshal explanation: %w", err)
	}
	policyRef, err := json.Marshal(dec.Policy)
	if err != nil {
		return fmt.Errorf("marshal decision policy: %w", err)
	}
	evidenceRefs, err := json.Marshal(toAnySlice(dec.EvidenceRefs))
	if err != nil {
		return fmt.Errorf("marshal evidence refs: %w", err)
	}
	_, err = tx.Exec(ctx,
		`INSERT INTO ctrl.decisions (tenant_id, id, seq, subject_type, subject_id, verb,
		   confidence, policy, explanation, evidence_refs, inputs_hash, rendered_at)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)
		 ON CONFLICT (id) DO NOTHING`,
		dec.TenantID, dec.ID, seq, string(dec.SubjectType), dec.SubjectID,
		string(dec.Verb), dec.Confidence, policyRef, explanation, evidenceRefs,
		dec.InputsHash, dec.RenderedAt)
	if err != nil {
		return fmt.Errorf("insert decision: %w", err)
	}
	return nil
}

// MarkPlanSatisfiedTx advances an active plan to satisfied inside a tx
// (plan.satisfied transition; terminal afterwards per I-08).
func MarkPlanSatisfiedTx(ctx context.Context, tx pgx.Tx, tenantID, planID string, seq int64) error {
	tag, err := tx.Exec(ctx,
		`UPDATE ctrl.validation_plans SET state='satisfied', seq=$3
		 WHERE id=$1 AND tenant_id=$2 AND state='active'`,
		planID, tenantID, seq)
	if err != nil {
		return fmt.Errorf("mark plan satisfied: %w", err)
	}
	_ = tag
	return nil
}

// MarkCandidateStateTx moves a candidate to the named state when its current
// state allows it (guarded UPDATE prevents resurrection of terminal states).
func MarkCandidateStateTx(ctx context.Context, tx pgx.Tx, tenantID, candidateID, state string, fromStates []string, seq int64) error {
	tag, err := tx.Exec(ctx,
		`UPDATE ctrl.candidates SET state=$3, seq=$4
		 WHERE id=$1 AND tenant_id=$2 AND state = ANY($5)`,
		candidateID, tenantID, state, seq, toTextSlice(fromStates))
	if err != nil {
		return fmt.Errorf("mark candidate %s: %w", state, err)
	}
	_ = tag
	return nil
}
