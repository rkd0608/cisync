package api

import (
	"net/http"
	"time"

	"cisync.dev/cisync/control-plane/internal/domain"
)

// handleGetIntent implements GET /v1/change-intents/{intentId}.
func (s *Server) handleGetIntent(w http.ResponseWriter, r *http.Request) {
	tenant := TenantFrom(r.Context())
	intentID := r.PathValue("intentId")
	intent, err := s.store.GetIntent(r.Context(), tenant, intentID)
	if err != nil {
		WriteDomainError(w, err)
		s.metrics.Inc("cisync_ctrl_http_requests_total", "404")
		return
	}
	pct := s.evidenceCompleteness(r, tenant, intent.ID)
	WriteJSON(w, http.StatusOK, intentToJSON(intent, pct))
	s.metrics.Inc("cisync_ctrl_http_requests_total", "200")
}

// evidenceCompleteness applies the D8 formula:
// len(accepted ∩ required_kinds) / len(required_kinds).
func (s *Server) evidenceCompleteness(r *http.Request, tenantID, intentID string) float64 {
	cands, err := s.store.ListCandidates(r.Context(), tenantID, intentID)
	if err != nil || len(cands) == 0 {
		return 0
	}
	requiredSet := map[string]bool{}
	for _, c := range cands {
		plan, err := s.store.ActivePlanForCandidate(r.Context(), tenantID, c.ID)
		if err != nil {
			continue
		}
		for _, k := range plan.RequiredEvidenceKinds {
			requiredSet[k] = true
		}
	}
	if len(requiredSet) == 0 {
		return 0
	}
	accepted := map[string]bool{}
	for _, c := range cands {
		evs, err := s.store.AcceptedEvidenceForCandidate(r.Context(), tenantID, c.ID)
		if err != nil {
			continue
		}
		for _, e := range evs {
			accepted[e.Kind] = true
		}
	}
	hit := 0
	for k := range requiredSet {
		if accepted[k] {
			hit++
		}
	}
	return float64(hit) / float64(len(requiredSet))
}

// intentJSON matches openapi Intent.
type intentJSON struct {
	IntentID                string     `json:"intent_id"`
	State                   string     `json:"state"`
	Goal                    string     `json:"goal"`
	Repository              string     `json:"repository"`
	OwnedSurfaces           []string   `json:"owned_surfaces"`
	RiskClass               string     `json:"risk_class"`
	EvidenceCompletenessPct float64    `json:"evidence_completeness_pct"`
	Deadline                *string    `json:"deadline"`
	CreatedAt               time.Time  `json:"created_at"`
	ClosedAt                *time.Time `json:"closed_at"`
}

func intentToJSON(i *domain.Intent, completeness float64) intentJSON {
	var deadline *string
	if i.Declared.Deadline != nil {
		f := i.Declared.Deadline.Format(time.RFC3339)
		deadline = &f
	}
	return intentJSON{
		IntentID:                i.ID,
		State:                   string(i.State),
		Goal:                    i.Declared.Goal,
		Repository:              i.Declared.Repo,
		OwnedSurfaces:           i.Declared.OwnedSurfaces,
		RiskClass:               string(i.Declared.RiskClass),
		EvidenceCompletenessPct: completeness,
		Deadline:                deadline,
		CreatedAt:               i.CreatedAt.UTC(),
		ClosedAt:                utcPtr(i.ClosedAt),
	}
}

func utcPtr(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	u := t.UTC()
	return &u
}
