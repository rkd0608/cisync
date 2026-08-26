package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"

	"cisync.dev/cisync/control-plane/internal/domain"
	plannerengine "cisync.dev/cisync/control-plane/internal/planner"
	"cisync.dev/cisync/control-plane/internal/store"
)

// revalidateRequest matches the openapi RevalidateRequest body.
type revalidateRequest struct {
	Reason string `json:"reason"`
}

// handleRevalidate implements POST /v1/candidates/{candidateId}/revalidate
// (wave-5 deliverable #1): append a fresh plan under CURRENT policy for the
// candidate's CURRENT inputs, increment rerun_count; 409 once the budget is
// exhausted or the candidate is terminal.
func (s *Server) handleRevalidate(w http.ResponseWriter, r *http.Request) {
	tenant := TenantFrom(r.Context())
	candidateID := r.PathValue("candidateId")
	raw, ok := s.readRawBody(w, r)
	if !ok {
		return
	}
	key := r.Header.Get("Idempotency-Key")
	if len(key) < 16 || len(key) > 128 {
		WriteError(w, http.StatusBadRequest, "validation_failed", "Idempotency-Key must be 16..128 chars", nil, nil, nil)
		return
	}
	endpoint := "POST /v1/candidates/{candidateId}/revalidate:" + candidateID
	reqHash := requestHash(raw)
	cached, err := s.store.LookupCommand(r.Context(), tenant, endpoint, key, reqHash)
	if err != nil {
		WriteDomainError(w, err)
		return
	}
	if cached != nil {
		writeRawJSON(w, cached.ResponseCode, cached.ResponseBody)
		s.metrics.Inc("cisync_ctrl_http_requests_total", "202")
		return
	}
	var in revalidateRequest
	if err := json.Unmarshal(raw, &in); err != nil && len(raw) > 0 {
		WriteError(w, http.StatusBadRequest, "validation_failed", "invalid JSON body", nil, nil, nil)
		return
	}

	var planID string
	err = s.store.ExecTx(r.Context(), func(tx pgx.Tx) error {
		cand, err := s.store.GetCandidate(r.Context(), tenant, candidateID)
		if err != nil {
			return err
		}
		intent, err := s.store.GetIntent(r.Context(), tenant, cand.IntentID)
		if err != nil {
			return err
		}
		plan, runs, err := s.buildReplan(r.Context(), intent, cand, domain.DefaultPolicy())
		if err != nil {
			return err
		}
		events, err := s.store.AppendReplanTx(r.Context(), tx, plan, runs)
		if err != nil {
			return err
		}
		s.metrics.Add("cisync_ctrl_events_appended_total", float64(len(events)))
		// Budget gate LAST inside the tx: a conditional bump makes
		// concurrent revalidations race-safe — only winners below cap commit.
		tag, err := tx.Exec(r.Context(),
			`UPDATE ctrl.candidates SET rerun_count=rerun_count+1
			 WHERE id=$1 AND tenant_id=$2 AND rerun_count < $3
			   AND state NOT IN ('eligible','rejected','superseded','cancelled')`,
			candidateID, tenant, s.cfg.RerunMaxPerCandidate)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return domain.ErrRerunBudgetExhausted
		}
		body, merr := json.Marshal(map[string]any{"plan_id": plan.ID})
		if merr != nil {
			return merr
		}
		planID = plan.ID
		return store.RecordCommandTx(r.Context(), tx, tenant, endpoint, key, reqHash,
			http.StatusAccepted, body)
	})
	if errors.Is(err, domain.ErrRerunBudgetExhausted) {
		WriteError(w, http.StatusConflict, "conflict_state",
			"re-run budget exhausted for this candidate",
			map[string]any{"reason": "rerun_budget_exhausted"}, nil, nil)
		s.metrics.Inc("cisync_ctrl_http_requests_total", "409")
		return
	}
	if err != nil {
		WriteDomainError(w, err)
		return
	}
	writeRawJSON(w, http.StatusAccepted, mustMarshal(map[string]any{"plan_id": planID}))
	s.metrics.Inc("cisync_ctrl_http_requests_total", "202")
}

// buildReplan plans afresh under CURRENT policy with the candidate's CURRENT
// inputs: identical inputs_hash keeps prior accepted evidence attributable
// (I-02) while the current policy pack re-earns every required kind.
func (s *Server) buildReplan(ctx context.Context, intent *domain.Intent, cand *domain.Candidate, pol domain.ResolvedPolicy) (*domain.ValidationPlan, []*domain.ValidationRun, error) {
	now := time.Now().UTC()
	plan, err := s.planner.Plan(ctx, domain.CandidateInput{
		CandidateID: cand.ID,
		IntentID:    cand.IntentID,
		Repo:        intent.Declared.Repo,
		BaseSHA:     cand.BaseSHA,
		HeadSHA:     cand.HeadSHA,
		PatchRef:    cand.PatchRef,
		RiskClass:   intent.Declared.RiskClass,
	}, pol)
	if err != nil {
		return nil, nil, err
	}
	// Fresh identity for the replan; the engine already resolved the CURRENT
	// active policy (I-09) and computed the deterministic inputs_hash.
	plan.ID = domain.NewID(domain.PrefixPlan)
	plan.TenantID = cand.TenantID
	plan.CreatedAt = now
	base := riskPriority[intent.Declared.RiskClass]
	var runs []*domain.ValidationRun
	for _, tier := range plan.Tiers {
		def := tierDefaults[tier.Tier]
		for _, jobName := range tier.Jobs {
			spec := jobSpecFor(intent, cand, plan)
			spec.Kind = plannerengine.EvidenceKindForJob(jobName)
			runs = append(runs, domain.NewValidationRun(domain.NewID(domain.PrefixRun),
				cand.TenantID, plan.ID, cand.ID, tier.Tier, spec, "sim",
				def.durationMS, def.costMC, base*float64(10-tier.Tier)/10, now))
		}
	}
	return plan, runs, nil
}
