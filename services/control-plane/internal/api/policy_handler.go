package api

import (
	"encoding/json"
	"net/http"
	"time"
)

// policyView is one served policy projection row (openapi PoliciesActive).
type policyView struct {
	PolicyID    string          `json:"policy_id"`
	Version     int             `json:"version"`
	Status      string          `json:"status"`
	ActivatedAt *time.Time      `json:"activated_at,omitempty"`
	Body        json.RawMessage `json:"body"`
}

// policiesResponse is the {policies:[...]} wrapper.
type policiesResponse struct {
	Policies []policyView `json:"policies"`
}

// handlePoliciesActive implements GET /v1/policies/active (+?history=1):
// a readonly read of the ctrl.policies projection. Active-only by default;
// history=1 returns every version including retired/draft.
func (s *Server) handlePoliciesActive(w http.ResponseWriter, r *http.Request) {
	tenant := TenantFrom(r.Context())
	history := r.URL.Query().Get("history") == "1"
	rows, err := s.store.PolicyProjections(r.Context(), tenant, history)
	if err != nil {
		WriteDomainError(w, err)
		return
	}
	policies := make([]policyView, 0, len(rows))
	for _, row := range rows {
		policies = append(policies, policyView{
			PolicyID:    row.PolicyID,
			Version:     row.Version,
			Status:      row.Status,
			ActivatedAt: row.ActivatedAt,
			Body:        row.Body,
		})
	}
	WriteJSON(w, http.StatusOK, policiesResponse{Policies: policies})
	s.metrics.Inc("sauron_ctrl_http_requests_total", "200")
}
