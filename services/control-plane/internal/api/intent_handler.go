package api

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"

	"sauron.dev/sauron/control-plane/internal/domain"
	"sauron.dev/sauron/control-plane/internal/store"
)

// intentCreate mirrors openapi IntentCreate.
type intentCreate struct {
	Goal               string   `json:"goal"`
	Repository         string   `json:"repository"`
	Base               string   `json:"base"`
	ExpectedSurfaces   []string `json:"expected_surfaces"`
	AcceptanceCriteria []string `json:"acceptance_criteria"`
	Constraints        []string `json:"constraints"`
	Risk               string   `json:"risk"`
	Deadline           *string  `json:"deadline"`
	AgentLineage       []string `json:"agent_lineage"`
}

var validRisk = map[string]bool{"low": true, "medium": true, "high": true, "critical": true}

func validateIntentCreate(in *intentCreate) error {
	if in.Goal == "" || len(in.Goal) > 2000 {
		return fmt.Errorf("%w: goal required and ≤2000 chars", domain.ErrValidationFailed)
	}
	if in.Repository == "" {
		return fmt.Errorf("%w: repository required", domain.ErrValidationFailed)
	}
	if in.Base == "" {
		return fmt.Errorf("%w: base required", domain.ErrValidationFailed)
	}
	if len(in.ExpectedSurfaces) == 0 {
		return fmt.Errorf("%w: expected_surfaces must contain at least one glob", domain.ErrValidationFailed)
	}
	if !validRisk[in.Risk] {
		return fmt.Errorf("%w: risk must be one of low|medium|high|critical", domain.ErrValidationFailed)
	}
	var deadline *time.Time
	if in.Deadline != nil && *in.Deadline != "" {
		t, err := time.Parse(time.RFC3339, *in.Deadline)
		if err != nil {
			return fmt.Errorf("%w: deadline must be RFC3339", domain.ErrValidationFailed)
		}
		deadline = &t
	}
	in.Deadline = nil
	if deadline != nil {
		s := deadline.Format(time.RFC3339)
		in.Deadline = &s
	}
	return nil
}

// handleCreateIntent implements POST /v1/change-intents end-to-end:
// idempotency replay → rate limit → overlap admission search → policy
// resolution → intent.declared + lease.granted in one tx.
func (s *Server) handleCreateIntent(w http.ResponseWriter, r *http.Request) {
	tenant := TenantFrom(r.Context())
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

	cached, err := s.store.LookupCommand(r.Context(), tenant, "POST /v1/change-intents", key, reqHash)
	if err != nil {
		WriteDomainError(w, err)
		s.metrics.Inc("sauron_ctrl_http_requests_total", "409")
		return
	}
	if cached != nil {
		writeRawJSON(w, cached.ResponseCode, cached.ResponseBody)
		s.metrics.Inc("sauron_ctrl_http_requests_total", fmt.Sprint(cached.ResponseCode))
		return
	}

	ok, retryAfter, err := s.store.TakeToken(r.Context(), tenant, "intents_per_minute", s.cfg.RateLimitPerMin, s.cfg.RateLimitPerMin)
	if err != nil {
		WriteDomainError(w, err)
		return
	}
	if !ok {
		retry := int(retryAfter.Seconds())
		WriteError(w, http.StatusTooManyRequests, "rate_limited", "intent creation rate exceeded", nil, &retry, nil)
		s.metrics.Inc("sauron_ctrl_http_requests_total", "429")
		return
	}

	var in intentCreate
	if err := json.Unmarshal(raw, &in); err != nil {
		WriteError(w, http.StatusBadRequest, "validation_failed", "invalid JSON body", nil, nil, nil)
		return
	}
	if err := validateIntentCreate(&in); err != nil {
		WriteDomainError(w, err)
		s.metrics.Inc("sauron_ctrl_http_requests_total", "400")
		return
	}

	pol := domain.DefaultPolicy()
	risk := domain.RiskClass(in.Risk)
	requiredEvidence, resolvable := pol.RequiredEvidence(risk)
	if !resolvable {
		WriteError(w, http.StatusBadRequest, "validation_failed", "risk class not covered by any active policy", nil, nil, nil)
		return
	}

	conflicts, err := s.store.OverlappingIntents(r.Context(), tenant, in.Repository, in.ExpectedSurfaces)
	if err != nil {
		WriteDomainError(w, err)
		return
	}

	now := time.Now().UTC()
	intentID := domain.NewID(domain.PrefixIntent)
	leaseID := domain.NewID(domain.PrefixLease)
	declared := domain.IntentDeclared{
		Goal:               in.Goal,
		Repo:               in.Repository,
		BaseRef:            in.Base,
		BaseSnapshot:       baseSnapshot(in.Repository, in.Base),
		OwnedSurfaces:      in.ExpectedSurfaces,
		Constraints:        in.Constraints,
		AcceptanceCriteria: in.AcceptanceCriteria,
		RiskClass:          risk,
		Origin:             domain.OriginAgentAPI,
		AgentLineage:       in.AgentLineage,
		ResolvedPolicy:     pol.Ref,
		ComputeBudget:      pol.PerCandidateBudget,
	}
	if in.Deadline != nil {
		t, _ := time.Parse(time.RFC3339, *in.Deadline)
		declared.Deadline = &t
	}
	intent := domain.NewIntent(intentID, tenant, declared, now)
	lease := domain.NewLease(leaseID, tenant, intentID,
		domain.LeaseScope{Kind: domain.ScopeChangeScope, Surfaces: in.ExpectedSurfaces},
		"agent:"+tenant, pol.PerCandidateBudget, s.cfg.DefaultLeaseTTL, requiredEvidence, now)

	prohibited := intersectMissing(pol.ProtectedPaths, in.ExpectedSurfaces)
	grant := buildIntentGrant(intent, lease, conflicts, prohibited, nil, nil)

	err = s.store.ExecTx(r.Context(), func(tx pgx.Tx) error {
		events, err := store.CreateIntentTx(r.Context(), tx, s.store, intent, lease, conflicts)
		if err != nil {
			return err
		}
		body, err := json.Marshal(grant)
		if err != nil {
			return err
		}
		s.metrics.Add("sauron_ctrl_events_appended_total", float64(len(events)))
		return store.RecordCommandTx(r.Context(), tx, tenant, "POST /v1/change-intents", key, reqHash, http.StatusOK, body)
	})
	if err != nil {
		WriteDomainError(w, err)
		s.metrics.Inc("sauron_ctrl_http_requests_total", "500")
		return
	}
	body, _ := json.Marshal(grant)
	writeRawJSON(w, http.StatusOK, body)
	s.metrics.Inc("sauron_ctrl_http_requests_total", "200")
}

// baseSnapshot derives a deterministic pseudo snapshot tag for the dev slice
// (no git access server-side yet).
func baseSnapshot(repo, base string) string {
	sum := sha256.Sum256([]byte(repo + "\x00" + base))
	return base + "@" + hex.EncodeToString(sum[:])[:7]
}

// intersectMissing returns protected paths not already allowed.
func intersectMissing(protected, allowed []string) []string {
	set := map[string]bool{}
	for _, a := range allowed {
		set[a] = true
	}
	out := []string{}
	for _, p := range protected {
		if !set[p] {
			out = append(out, p)
		}
	}
	return out
}

// intentGrantJSON matches openapi IntentGrant key-for-key.
type intentGrantJSON struct {
	IntentID         string         `json:"intent_id"`
	LeaseID          string         `json:"lease_id"`
	BaseSnapshot     string         `json:"base_snapshot"`
	WorktreeOrBranch string         `json:"worktree_or_branch"`
	AllowedPaths     []string       `json:"allowed_paths"`
	ProhibitedPaths  []string       `json:"prohibited_paths"`
	Conflicts        []conflictJSON `json:"conflicts"`
	RequiredEvidence []string       `json:"required_evidence"`
	ComputeBudget    budgetJSON     `json:"compute_budget"`
	QueuePosition    *int           `json:"queue_position"`
	EtaSeconds       *int           `json:"eta_seconds"`
}

type conflictJSON struct {
	IntentID       string `json:"intent_id"`
	Relation       string `json:"relation"`
	Owner          string `json:"owner"`
	Recommendation string `json:"recommendation"`
}

type budgetJSON struct {
	CPUMinutes         int64 `json:"cpu_minutes"`
	EnvironmentMinutes int64 `json:"environment_minutes"`
	RepairAttempts     int64 `json:"repair_attempts"`
}

func buildIntentGrant(intent *domain.Intent, lease *domain.Lease, conflicts []domain.ConflictRef, prohibited []string, queuePos *int, eta *int) intentGrantJSON {
	cf := make([]conflictJSON, 0, len(conflicts))
	for _, c := range conflicts {
		cf = append(cf, conflictJSON{IntentID: c.IntentID, Relation: c.Relation, Owner: c.Owner, Recommendation: c.Recommendation})
	}
	prohibitedOut := prohibited
	if prohibitedOut == nil {
		prohibitedOut = []string{}
	}
	return intentGrantJSON{
		IntentID:         intent.ID,
		LeaseID:          lease.ID,
		BaseSnapshot:     intent.Declared.BaseSnapshot,
		WorktreeOrBranch: "agent/" + intent.ID + "/candidate_01",
		AllowedPaths:     intent.Declared.OwnedSurfaces,
		ProhibitedPaths:  prohibitedOut,
		Conflicts:        cf,
		RequiredEvidence: lease.RequiredEvidence,
		ComputeBudget: budgetJSON{
			CPUMinutes:         lease.Budget.CPUMinutes,
			EnvironmentMinutes: lease.Budget.EnvironmentMinutes,
			RepairAttempts:     lease.Budget.RepairAttempts,
		},
		QueuePosition: queuePos,
		EtaSeconds:    eta,
	}
}

func writeRawJSON(w http.ResponseWriter, status int, body []byte) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(body)
	_, _ = w.Write([]byte("\n"))
}
