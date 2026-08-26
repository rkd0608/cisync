package api

import (
	"net/http"
	"time"

	"cisync.dev/cisync/control-plane/internal/domain"
)

// handleGetCandidate implements GET /v1/candidates/{candidateId}.
func (s *Server) handleGetCandidate(w http.ResponseWriter, r *http.Request) {
	tenant := TenantFrom(r.Context())
	candidateID := r.PathValue("candidateId")
	cand, err := s.store.GetCandidate(r.Context(), tenant, candidateID)
	if err != nil {
		WriteDomainError(w, err)
		return
	}
	pos, err := s.store.QueuePositionForCandidate(r.Context(), tenant, candidateID)
	if err != nil {
		WriteDomainError(w, err)
		return
	}
	out := candidateToSummary(cand)
	resp := struct {
		candidateSummaryJSON
		IntentID          string `json:"intent_id"`
		QueuePosition     *int   `json:"queue_position"`
		EstCostMillicents int64  `json:"est_cost_millicents"`
	}{candidateSummaryJSON: out, IntentID: cand.IntentID, QueuePosition: pos, EstCostMillicents: cand.EstCostMillicents}
	WriteJSON(w, http.StatusOK, resp)
	s.metrics.Inc("cisync_ctrl_http_requests_total", "200")
}

// handleGetDossier implements GET /v1/candidates/{candidateId}/dossier; it
// 404s until a decision has been rendered (fail-closed, no fabrication).
func (s *Server) handleGetDossier(w http.ResponseWriter, r *http.Request) {
	tenant := TenantFrom(r.Context())
	candidateID := r.PathValue("candidateId")
	cand, err := s.store.GetCandidate(r.Context(), tenant, candidateID)
	if err != nil {
		WriteDomainError(w, err)
		return
	}
	inputsHash, err := s.store.InputsHashForCandidate(r.Context(), tenant, candidateID)
	if err != nil {
		WriteDomainError(w, domain.ErrNotFound)
		return
	}
	decision, err := s.store.LatestDecisionForCandidate(r.Context(), tenant, candidateID)
	if err != nil {
		if err == domain.ErrNotFound {
			retry := 30
			WriteError(w, http.StatusNotFound, "not_found",
				"no decision rendered yet for this candidate", nil, &retry, nil)
			s.metrics.Inc("cisync_ctrl_http_requests_total", "404")
			return
		}
		WriteDomainError(w, err)
		return
	}
	evs, err := s.store.AcceptedEvidenceForCandidate(r.Context(), tenant, candidateID)
	if err != nil {
		WriteDomainError(w, err)
		return
	}

	accepted := make([]dossierEvidenceJSON, 0, len(evs))
	for _, e := range evs {
		digests := e.Digests
		if digests == nil {
			digests = []string{}
		}
		accepted = append(accepted, dossierEvidenceJSON{
			EvID: e.ID, Kind: e.Kind, Verdict: e.Verdict, Digests: digests, Meta: map[string]any{},
		})
	}
	now := time.Now().UTC()
	dossier := dossierJSON{
		CandidateID: cand.ID,
		IntentID:    cand.IntentID,
		GeneratedAt: now,
		InputsHash:  inputsHash,
		Decision: dossierDecisionJSON{
			DecisionID: decision.ID,
			Verb:       decision.Verb,
			Confidence: decision.Confidence,
			Policy:     dossierPolicyJSON{PolicyID: decision.Policy.PolicyID, Version: decision.Policy.Version},
			Summary:    decision.Summary,
		},
		EvidenceAccepted:  accepted,
		EvidenceDeferred:  []dossierDeferredJSON{},
		KnownUncertainty:  []map[string]string{},
		RequiredPostMerge: []map[string]any{},
	}
	WriteJSON(w, http.StatusOK, dossier)
	s.metrics.Inc("cisync_ctrl_http_requests_total", "200")
}

// dossierPolicyJSON matches openapi EvidenceDossier.decision.policy
// key-for-key: the REST shape spells the version field "version" (the event
// schema's PolicyRef uses "policy_version" — they are intentionally distinct).
type dossierPolicyJSON struct {
	PolicyID string `json:"policy_id"`
	Version  int    `json:"version"`
}

type dossierDecisionJSON struct {
	DecisionID string            `json:"decision_id"`
	Verb       string            `json:"verb"`
	Confidence float64           `json:"confidence"`
	Policy     dossierPolicyJSON `json:"policy"`
	Summary    string            `json:"summary"`
}

type dossierEvidenceJSON struct {
	EvID    string         `json:"ev_id"`
	Kind    string         `json:"kind"`
	Verdict string         `json:"verdict"`
	Digests []string       `json:"digests,omitempty"`
	Meta    map[string]any `json:"meta,omitempty"`
}

type dossierDeferredJSON struct {
	Kind          string `json:"kind"`
	Reason        string `json:"reason"`
	StageRequired string `json:"stage_required"`
}

type dossierJSON struct {
	CandidateID       string                `json:"candidate_id"`
	IntentID          string                `json:"intent_id"`
	GeneratedAt       time.Time             `json:"generated_at"`
	InputsHash        string                `json:"inputs_hash"`
	Decision          dossierDecisionJSON   `json:"decision"`
	EvidenceAccepted  []dossierEvidenceJSON `json:"evidence_accepted"`
	EvidenceDeferred  []dossierDeferredJSON `json:"evidence_deferred"`
	KnownUncertainty  []map[string]string   `json:"known_uncertainty"`
	RequiredPostMerge []map[string]any      `json:"required_post_merge"`
}
