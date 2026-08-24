package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"sauron.dev/sauron/control-plane/internal/domain"
)

// handleGetCluster implements GET /v1/clusters/{clusterId}.
func (s *Server) handleGetCluster(w http.ResponseWriter, r *http.Request) {
	tenant := TenantFrom(r.Context())
	clusterID := r.PathValue("clusterId")
	cluster, members, err := s.store.GetCluster(r.Context(), tenant, clusterID)
	if err != nil {
		WriteDomainError(w, err)
		return
	}
	memberOut := make([]clusterMemberJSON, 0, len(members))
	for _, m := range members {
		memberOut = append(memberOut, clusterMemberJSON{
			CandidateID: m.CandidateID, RelationToRep: string(m.RelationToRep), SimilarityScore: m.SimilarityScore,
		})
	}
	WriteJSON(w, http.StatusOK, clusterJSON{
		ClusterID:       cluster.ID,
		Repo:            cluster.Repo,
		RepCandidateID:  cluster.RepCandidateID,
		MemberCount:     cluster.MemberCount,
		State:           string(cluster.State),
		StrategyVersion: cluster.StrategyVersion,
		Members:         memberOut,
	})
	s.metrics.Inc("sauron_ctrl_http_requests_total", "200")
}

type clusterMemberJSON struct {
	CandidateID     string  `json:"candidate_id"`
	RelationToRep   string  `json:"relation_to_rep"`
	SimilarityScore float64 `json:"similarity_score"`
}

type clusterJSON struct {
	ClusterID       string              `json:"cluster_id"`
	Repo            string              `json:"repo"`
	RepCandidateID  string              `json:"rep_candidate_id"`
	MemberCount     int                 `json:"member_count"`
	State           string              `json:"state"`
	StrategyVersion string              `json:"strategy_version"`
	Members         []clusterMemberJSON `json:"members"`
}

// handleRenewLease implements POST /v1/leases/{leaseId}/renew.
func (s *Server) handleRenewLease(w http.ResponseWriter, r *http.Request) {
	tenant := TenantFrom(r.Context())
	leaseID := r.PathValue("leaseId")
	raw, ok := s.readRawBody(w, r)
	if !ok {
		return
	}
	key := r.Header.Get("Idempotency-Key")
	if len(key) < 16 || len(key) > 128 {
		WriteError(w, http.StatusBadRequest, "validation_failed", "Idempotency-Key must be 16..128 chars", nil, nil, nil)
		return
	}
	cached, err := s.store.LookupCommand(r.Context(), tenant, "POST /v1/leases/{leaseId}/renew:"+leaseID, key, requestHash(raw))
	if err != nil {
		WriteDomainError(w, err)
		return
	}
	if cached != nil {
		writeRawJSON(w, cached.ResponseCode, cached.ResponseBody)
		return
	}

	var body struct {
		TTLSeconds int64 `json:"ttl_seconds"`
	}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &body); err != nil {
			WriteError(w, http.StatusBadRequest, "validation_failed", "invalid JSON body", nil, nil, nil)
			return
		}
	}
	if body.TTLSeconds == 0 {
		body.TTLSeconds = 1500
	}
	// P1-3: domain validation first (bounds check), then the conditional
	// UPDATE — no GetLease→mutate→persist window; expired/revoked leases
	// come back as typed conflict from the store.
	if body.TTLSeconds < 30 || body.TTLSeconds > 3600 {
		WriteError(w, http.StatusBadRequest, "validation_failed",
			"ttl_seconds must be within [30,3600]", nil, nil, nil)
		return
	}
	ttlExpiresAt, renewalCount, err := s.store.RenewLease(r.Context(), tenant, leaseID, body.TTLSeconds)
	if err != nil {
		if errors.Is(err, domain.ErrConflict) {
			WriteError(w, http.StatusConflict, "conflict_state",
				"lease is not renewable; request a fresh grant",
				map[string]any{"reason": "expired_lease"}, nil, nil)
			s.metrics.Inc("sauron_ctrl_http_requests_total", "409")
			return
		}
		if errors.Is(err, domain.ErrNotFound) {
			WriteDomainError(w, err)
			return
		}
		WriteDomainError(w, err)
		return
	}
	resp, _ := json.Marshal(map[string]any{
		"lease_id":       leaseID,
		"ttl_expires_at": ttlExpiresAt.Format(time.RFC3339),
		"renewal_count":  renewalCount,
	})
	writeRawJSON(w, http.StatusOK, resp)
	s.metrics.Inc("sauron_ctrl_http_requests_total", "200")
}

// handleReleaseLease implements DELETE /v1/leases/{leaseId}; idempotent.
func (s *Server) handleReleaseLease(w http.ResponseWriter, r *http.Request) {
	tenant := TenantFrom(r.Context())
	leaseID := r.PathValue("leaseId")
	lease, err := s.store.GetLease(r.Context(), tenant, leaseID)
	if err != nil {
		if err == domain.ErrNotFound {
			w.WriteHeader(http.StatusNoContent)
			s.metrics.Inc("sauron_ctrl_http_requests_total", "204")
			return
		}
		WriteDomainError(w, err)
		return
	}
	if lease.State == domain.LeaseGranted {
		if applyErr := lease.Apply("lease.released"); applyErr == nil {
			if relErr := s.store.ReleaseLease(r.Context(), lease, "intent_terminal"); relErr != nil {
				WriteDomainError(w, relErr)
				return
			}
		}
	}
	w.WriteHeader(http.StatusNoContent)
	s.metrics.Inc("sauron_ctrl_http_requests_total", "204")
}
