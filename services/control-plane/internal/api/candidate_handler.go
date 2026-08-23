package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"

	"sauron.dev/sauron/control-plane/internal/cluster"
	"sauron.dev/sauron/control-plane/internal/domain"
	plannerengine "sauron.dev/sauron/control-plane/internal/planner"
	"sauron.dev/sauron/control-plane/internal/store"
)

// candidateSubmit mirrors openapi CandidateSubmit.
type candidateSubmit struct {
	PatchRef     string   `json:"patch_ref"`
	HeadSHA      string   `json:"head_sha"`
	BaseSHA      string   `json:"base_sha"`
	ChangedPaths []string `json:"changed_paths"`
}

// handleListCandidates implements GET /v1/change-intents/{intentId}/candidates.
func (s *Server) handleListCandidates(w http.ResponseWriter, r *http.Request) {
	tenant := TenantFrom(r.Context())
	intentID := r.PathValue("intentId")
	if _, err := s.store.GetIntent(r.Context(), tenant, intentID); err != nil {
		WriteDomainError(w, err)
		s.metrics.Inc("sauron_ctrl_http_requests_total", "404")
		return
	}
	cands, err := s.store.ListCandidates(r.Context(), tenant, intentID)
	if err != nil {
		WriteDomainError(w, err)
		return
	}
	out := make([]candidateSummaryJSON, 0, len(cands))
	for _, c := range cands {
		out = append(out, candidateToSummary(c))
	}
	WriteJSON(w, http.StatusOK, out)
	s.metrics.Inc("sauron_ctrl_http_requests_total", "200")
}

// handleSubmitCandidate implements POST /v1/change-intents/{intentId}/candidates:
// validation → duplicate guard → Planner port → plan + queued runs atomically.
func (s *Server) handleSubmitCandidate(w http.ResponseWriter, r *http.Request) {
	tenant := TenantFrom(r.Context())
	intentID := r.PathValue("intentId")
	raw, ok := s.readRawBody(w, r)
	if !ok {
		return
	}
	key := r.Header.Get("Idempotency-Key")
	if len(key) < 16 || len(key) > 128 {
		WriteError(w, http.StatusBadRequest, "validation_failed", "Idempotency-Key must be 16..128 chars", nil, nil, nil)
		s.metrics.Inc("sauron_ctrl_http_requests_total", "400")
		return
	}
	reqHash := requestHash(raw)

	endpoint := "POST /v1/change-intents/{intentId}/candidates:" + intentID
	cached, err := s.store.LookupCommand(r.Context(), tenant, endpoint, key, reqHash)
	if err != nil {
		WriteDomainError(w, err)
		return
	}
	if cached != nil {
		writeRawJSON(w, cached.ResponseCode, cached.ResponseBody)
		s.metrics.Inc("sauron_ctrl_http_requests_total", fmt.Sprint(cached.ResponseCode))
		return
	}

	intent, in, ok := s.decodeCandidateSubmit(w, r, intentID, raw)
	if !ok {
		return
	}

	pol := domain.DefaultPolicy()
	now := time.Now().UTC()
	candID := domain.NewID(domain.PrefixCandidate)
	changedPaths := in.ChangedPaths
	if changedPaths == nil {
		changedPaths = []string{}
	}
	cand, err := domain.NewCandidate(candID, tenant, intent.ID, "agent:"+tenant,
		in.PatchRef, in.HeadSHA, in.BaseSHA, changedPaths, 0, now)
	if err != nil {
		WriteDomainError(w, err)
		return
	}
	plan, err := s.planner.Plan(r.Context(), domain.CandidateInput{
		CandidateID:  cand.ID,
		IntentID:     intent.ID,
		Repo:         intent.Declared.Repo,
		BaseSHA:      in.BaseSHA,
		HeadSHA:      in.HeadSHA,
		PatchRef:     in.PatchRef,
		ChangedPaths: in.ChangedPaths,
		RiskClass:    intent.Declared.RiskClass,
	}, pol)
	if err != nil {
		WriteDomainError(w, err)
		return
	}
	plan.TenantID = tenant

	base := riskPriority[intent.Declared.RiskClass]
	var runs []*domain.ValidationRun
	for _, tier := range plan.Tiers {
		def := tierDefaults[tier.Tier]
		// One run per producing job keeps the evidence ledger honest: each
		// completed run may satisfy exactly its own required kind (I-03).
		for _, jobName := range tier.Jobs {
			spec := jobSpecFor(intent, cand, plan)
			spec.Kind = plannerengine.EvidenceKindForJob(jobName)
			run := domain.NewValidationRun(domain.NewID(domain.PrefixRun), tenant, plan.ID, cand.ID,
				tier.Tier, spec, "sim", def.durationMS, def.costMC,
				base*float64(10-tier.Tier)/10, now)
			runs = append(runs, run)
			cand.EstCostMillicents += def.costMC
		}
	}

	// Cluster assignment at submission (§2): join iff path-overlap ≥ 0.6 AND
	// trigram similarity ≥ τ against an active representative; duplicates are
	// parked as blocked_representative until the representative resolves.
	assignment, repID := assignCluster(s.store, tenant, intent.Declared.Repo, cand.ID,
		changedPaths, base)
	cand.ClusterID = assignment.ClusterID
	if assignment.RelationToRep != "" {
		rel := domain.Relation(assignment.RelationToRep)
		cand.RelationToRep = &rel
	}
	if assignment.RelationToRep == cluster.RelationDuplicateOf {
		cand.State = domain.CandBlockedRepresentative
	}
	assignmentData := store.ClusterAssignmentData{
		Joined:            assignment.Joined,
		ClusterID:         assignment.ClusterID,
		Repo:              intent.Declared.Repo,
		RelationToRep:     assignment.RelationToRep,
		PathOverlap:       assignment.PathOverlap,
		TrigramSimilarity: assignment.TrigramSimilarity,
		SymbolOverlap:     assignment.SymbolOverlap,
		StrategyVersion:   assignment.StrategyVersion,
	}
	if assignment.Joined {
		assignmentData.RepCandidateID = repID
	} else {
		// Unclustered candidate seeds a fresh singleton cluster it represents.
		assignmentData.ClusterID = domain.NewID(domain.PrefixCluster)
		assignmentData.RepCandidateID = cand.ID
		cand.ClusterID = assignmentData.ClusterID
	}
	leaseID := ""
	leases, err := s.store.LeaseForIntent(r.Context(), tenant, intent.ID)
	if err == nil && len(leases) > 0 {
		leaseID = leases[0].ID
	}

	err = s.store.ExecTx(r.Context(), func(tx pgx.Tx) error {
		events, err := store.SubmitCandidateTx(r.Context(), tx, s.store, cand, plan, runs, &assignmentData)
		if err != nil {
			return err
		}
		s.metrics.Add("sauron_ctrl_events_appended_total", float64(len(events)))
		body, err := json.Marshal(candidateAcceptedJSON{
			CandidateID: cand.ID,
			PlanSummary: planSummaryJSON(plan),
			LeaseID:     leaseID,
		})
		if err != nil {
			return err
		}
		return store.RecordCommandTx(r.Context(), tx, tenant, endpoint, key, reqHash, http.StatusCreated, body)
	})
	if err != nil {
		WriteDomainError(w, err)
		return
	}
	writeRawJSON(w, http.StatusCreated, mustMarshal(candidateAcceptedJSON{
		CandidateID: cand.ID,
		PlanSummary: planSummaryJSON(plan),
		LeaseID:     leaseID,
	}))
	s.metrics.Inc("sauron_ctrl_http_requests_total", "201")
}

// decodeCandidateSubmit performs the boundary validation shared by the
// submit path: idempotent-replay already handled by the caller.
func (s *Server) decodeCandidateSubmit(w http.ResponseWriter, r *http.Request, intentID string, raw []byte) (*domain.Intent, candidateSubmit, bool) {
	tenant := TenantFrom(r.Context())
	var in candidateSubmit
	if err := json.Unmarshal(raw, &in); err != nil {
		WriteError(w, http.StatusBadRequest, "validation_failed", "invalid JSON body", nil, nil, nil)
		return nil, in, false
	}
	if in.PatchRef == "" || len(in.HeadSHA) != 40 || len(in.BaseSHA) != 40 {
		WriteError(w, http.StatusBadRequest, "validation_failed",
			"patch_ref required; head_sha/base_sha must be 40 hex chars", nil, nil, nil)
		return nil, in, false
	}
	intent, err := s.store.GetIntent(r.Context(), tenant, intentID)
	if err != nil {
		WriteDomainError(w, err)
		return nil, in, false
	}
	switch intent.State {
	case domain.IntentExploring, domain.IntentValidating:
	default:
		WriteDomainError(w, fmt.Errorf("%w: intent state %s rejects submissions", domain.ErrLateSubmission, intent.State))
		return nil, in, false
	}
	dup, err := s.store.LiveCandidateExists(r.Context(), tenant, intent.ID, in.HeadSHA)
	if err != nil {
		WriteDomainError(w, err)
		return nil, in, false
	}
	if dup {
		WriteDomainError(w, domain.ErrDuplicateHead)
		return nil, in, false
	}
	return intent, in, true
}
