package store

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5"

	"cisync.dev/cisync/control-plane/internal/domain"
)

// IntentForPR resolves the synthetic/declared intent bound to a pull request
// via the (tenant_id, repo, pr_number) projection index (plan §3.3).
func (s *Store) IntentForPR(ctx context.Context, tenantID, repo string, prNumber int) (*domain.Intent, error) {
	row := s.Pool.QueryRow(ctx,
		`SELECT id FROM ctrl.intents
		 WHERE tenant_id=$1 AND repo=$2 AND pr_number=$3
		 ORDER BY created_at DESC LIMIT 1`, tenantID, repo, prNumber)
	var intentID string
	if err := row.Scan(&intentID); err != nil {
		if err == pgx.ErrNoRows {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("intent for pr: %w", err)
	}
	return s.GetIntent(ctx, tenantID, intentID)
}

// CandidateHeadState reports whether a head_sha is already known for an
// intent and, if so, whether its candidate is still live. The normalizer's
// out-of-order guard rides this: live match ⇒ replay; non-live match ⇒ stale.
func CandidateHeadStateTx(ctx context.Context, q Queryer, tenantID, intentID, headSHA string) (live bool, known bool, err error) {
	err = q.QueryRow(ctx,
		`SELECT COALESCE(bool_or(state NOT IN ('superseded','cancelled')), false), count(*) > 0
		 FROM ctrl.candidates WHERE tenant_id=$1 AND intent_id=$2 AND head_sha=$3`,
		tenantID, intentID, headSHA).Scan(&live, &known)
	if err != nil {
		return false, false, fmt.Errorf("candidate head state: %w", err)
	}
	return live, known, nil
}

// LiveCandidatesForIntent returns the intent's non-terminal candidates —
// the supersede/cancel targets of synchronize/close cascades.
func LiveCandidatesForIntentTx(ctx context.Context, q Queryer, tenantID, intentID string) ([]*domain.Candidate, error) {
	rows, err := q.Query(ctx,
		`SELECT id, intent_id, state, patch_ref, head_sha, base_sha FROM ctrl.candidates
		 WHERE tenant_id=$1 AND intent_id=$2
		   AND state NOT IN ('superseded','cancelled','rejected','eligible')
		 ORDER BY created_at`, tenantID, intentID)
	if err != nil {
		return nil, fmt.Errorf("live candidates: %w", err)
	}
	defer rows.Close()
	out := []*domain.Candidate{}
	for rows.Next() {
		cand := &domain.Candidate{}
		var state string
		if err := rows.Scan(&cand.ID, &cand.IntentID, &state, &cand.PatchRef,
			&cand.HeadSHA, &cand.BaseSHA); err != nil {
			return nil, fmt.Errorf("scan live candidate: %w", err)
		}
		cand.State = domain.CandidateState(state)
		cand.TenantID = tenantID
		out = append(out, cand)
	}
	return out, rows.Err()
}

// QueuedRunIDsTx lists the still-queued run ids of a candidate for
// validation.cancelled payloads (dispatched/running are fenced separately).
func QueuedRunIDsTx(ctx context.Context, q Queryer, tenantID, candidateID string) ([]string, error) {
	rows, err := q.Query(ctx,
		`SELECT id FROM ctrl.validation_runs
		 WHERE tenant_id=$1 AND candidate_id=$2 AND state='queued'`,
		tenantID, candidateID)
	return scanIDRows(rows, err)
}

// AcceptedEvidenceIDsTx lists accepted evidence ids of a candidate — the
// evidence.invalidated payload of a revision chain.
func AcceptedEvidenceIDsTx(ctx context.Context, q Queryer, tenantID, candidateID string) ([]string, error) {
	rows, err := q.Query(ctx,
		`SELECT id FROM ctrl.evidence_records
		 WHERE tenant_id=$1 AND candidate_id=$2 AND status='accepted'`,
		tenantID, candidateID)
	return scanIDRows(rows, err)
}

// ActiveRepairTaskIDsTx lists repair tasks still open on a candidate so the
// synchronize cascade can abort them with the revision (plan §3.3).
func ActiveRepairTaskIDsTx(ctx context.Context, q Queryer, tenantID, candidateID string) ([]string, error) {
	rows, err := q.Query(ctx,
		`SELECT id FROM ctrl.repair_tasks
		 WHERE tenant_id=$1 AND candidate_id=$2 AND state NOT IN ('applied','exhausted','aborted')`,
		tenantID, candidateID)
	return scanIDRows(rows, err)
}

// SupersedeCandidateTx flips one candidate row to superseded inside the
// caller's tx (the event was already appended by the effect builder).
func SupersedeCandidateTx(ctx context.Context, tx pgx.Tx, tenantID, candidateID string, seq int64) error {
	tag, err := tx.Exec(ctx,
		`UPDATE ctrl.candidates SET state='superseded', seq=$3
		 WHERE id=$1 AND tenant_id=$2 AND state NOT IN ('superseded','cancelled')`,
		candidateID, tenantID, seq)
	if err != nil {
		return fmt.Errorf("supersede candidate: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrPostTerminal
	}
	return nil
}

// CancelCandidateTx flips one candidate row to cancelled inside the tx.
func CancelCandidateTx(ctx context.Context, tx pgx.Tx, tenantID, candidateID string, seq int64) error {
	tag, err := tx.Exec(ctx,
		`UPDATE ctrl.candidates SET state='cancelled', seq=$3
		 WHERE id=$1 AND tenant_id=$2 AND state NOT IN ('superseded','cancelled')`,
		candidateID, tenantID, seq)
	if err != nil {
		return fmt.Errorf("cancel candidate: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrPostTerminal
	}
	return nil
}

// LiveCandidatesByRepoBase lists live candidates in a repo whose base_sha
// equals the pushed-from SHA — the merge_base.advanced target set.
func (s *Store) LiveCandidatesByRepoBase(ctx context.Context, tenantID, repo, oldBaseSHA string) ([]*domain.Candidate, error) {
	rows, err := s.Pool.Query(ctx,
		`SELECT id, intent_id, state FROM ctrl.candidates
		 WHERE tenant_id=$1 AND repo=$2 AND base_sha=$3 AND state NOT IN ('superseded','cancelled','rejected','eligible')
		 ORDER BY created_at`, tenantID, repo, oldBaseSHA)
	if err != nil {
		return nil, fmt.Errorf("live candidates by repo base: %w", err)
	}
	defer rows.Close()
	out := []*domain.Candidate{}
	for rows.Next() {
		cand := &domain.Candidate{Repo: repo}
		var state string
		if err := rows.Scan(&cand.ID, &cand.IntentID, &state); err != nil {
			return nil, fmt.Errorf("scan repo-base candidate: %w", err)
		}
		cand.State = domain.CandidateState(state)
		cand.TenantID = tenantID
		out = append(out, cand)
	}
	return out, rows.Err()
}

// SuspendReposTx upserts the G10 suspension flag for each repo.
func SuspendReposTx(ctx context.Context, tx pgx.Tx, repos []string) error {
	for _, repo := range repos {
		if _, err := tx.Exec(ctx,
			`INSERT INTO ctrl.repo_suspensions (repo) VALUES ($1)
			 ON CONFLICT (repo) DO NOTHING`, repo); err != nil {
			return fmt.Errorf("suspend repo %s: %w", repo, err)
		}
	}
	return nil
}

// RepoSuspended reports whether synthetic-intent creation is halted.
func (s *Store) RepoSuspended(ctx context.Context, repo string) (bool, error) {
	var suspended bool
	err := s.Pool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM ctrl.repo_suspensions WHERE repo=$1)`, repo).Scan(&suspended)
	if err != nil {
		return false, fmt.Errorf("repo suspended: %w", err)
	}
	return suspended, nil
}

// ActiveLeasesForRepos lists non-terminal leases whose intents belong to the
// given repos — the G10 revocation target set (budget released per lease).
type LeaseRevocation struct {
	LeaseID        string
	IntentID       string
	ReleasedBudget domain.BudgetValues
}

func (s *Store) ActiveLeasesForRepos(ctx context.Context, tenantID string, repos []string) ([]LeaseRevocation, error) {
	rows, err := s.Pool.Query(ctx,
		`SELECT l.id, i.id, i.compute_budget
		 FROM ctrl.leases l JOIN ctrl.intents i ON i.id = l.intent_id AND i.tenant_id = l.tenant_id
		 WHERE l.tenant_id=$1 AND i.repo = ANY($2) AND l.state IN ('requested','granted')`,
		tenantID, repos)
	if err != nil {
		return nil, fmt.Errorf("active leases for repos: %w", err)
	}
	defer rows.Close()
	out := []LeaseRevocation{}
	for rows.Next() {
		var lr LeaseRevocation
		var budgetRaw []byte
		if err := rows.Scan(&lr.LeaseID, &lr.IntentID, &budgetRaw); err != nil {
			return nil, fmt.Errorf("scan lease revocation: %w", err)
		}
		lr.ReleasedBudget = domain.DefaultPolicy().PerCandidateBudget
		if err := json.Unmarshal(budgetRaw, &lr.ReleasedBudget); err != nil {
			// Unparsable budgets keep the policy default rather than failing
			// the revocation sweep; revocation itself must never stall.
			lr.ReleasedBudget = domain.DefaultPolicy().PerCandidateBudget
		}
		out = append(out, lr)
	}
	return out, rows.Err()
}
