package api

import (
	"encoding/json"

	"cisync.dev/cisync/control-plane/internal/domain"
)

// riskPriority is the §4 static blast-radius seed by intent risk class.
var riskPriority = map[domain.RiskClass]float64{
	domain.RiskLow: 0.4, domain.RiskMedium: 0.7, domain.RiskHigh: 1.0, domain.RiskCritical: 1.0,
}

// tierDefaults per DOMAIN_MODEL_DRAFT §3 (duration ms, cost millicents).
var tierDefaults = map[int]struct {
	durationMS int64
	costMC     int64
}{
	0: {60000, 5000},
	1: {900000, 20000},
	2: {1800000, 150000},
	3: {3600000, 1200000},
	4: {5400000, 4000000},
}

func jobSpecFor(intent *domain.Intent, cand *domain.Candidate, plan *domain.ValidationPlan) domain.JobSpec {
	return domain.JobSpec{
		Kind:       "hermetic_build",
		Repo:       intent.Declared.Repo,
		BaseSHA:    cand.BaseSHA,
		HeadSHA:    cand.HeadSHA,
		PatchRef:   cand.PatchRef,
		InputsHash: plan.InputsHash,
		TimeoutMS:  900000,
		SimProfile: map[string]any{"duration_ms": 800, "outcome_bias": "pass"},
	}
}

// mustMarshal marshals v, falling back to an empty object rather than
// corrupting an already-committed HTTP transaction.
func mustMarshal(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		return []byte("{}")
	}
	return b
}

// candidateAcceptedJSON matches openapi CandidateAccepted.
type candidateAcceptedJSON struct {
	CandidateID string         `json:"candidate_id"`
	PlanSummary planSummaryOut `json:"plan_summary"`
	LeaseID     string         `json:"lease_id"`
}

type tierSummaryJSON struct {
	Tier                int      `json:"tier"`
	Jobs                []string `json:"jobs"`
	Rationale           string   `json:"rationale"`
	SelectionConfidence *float64 `json:"selection_confidence"`
}

type planSummaryOut struct {
	Tiers    []tierSummaryJSON `json:"tiers"`
	Deferred []string          `json:"deferred"`
}

func planSummaryJSON(p *domain.ValidationPlan) planSummaryOut {
	tiers := make([]tierSummaryJSON, 0, len(p.Tiers))
	for _, t := range p.Tiers {
		jobs := t.Jobs
		if jobs == nil {
			jobs = []string{}
		}
		tiers = append(tiers, tierSummaryJSON{Tier: t.Tier, Jobs: jobs, Rationale: t.Rationale, SelectionConfidence: t.SelectionConfidence})
	}
	return planSummaryOut{Tiers: tiers, Deferred: []string{}}
}

// candidateSummaryJSON matches openapi CandidateSummary.
type candidateSummaryJSON struct {
	CandidateID   string  `json:"candidate_id"`
	State         string  `json:"state"`
	HeadSHA       string  `json:"head_sha"`
	PriorityScore float64 `json:"priority_score"`
	ClusterID     *string `json:"cluster_id"`
	RelationToRep *string `json:"relation_to_rep"`
}

func candidateToSummary(c *domain.Candidate) candidateSummaryJSON {
	out := candidateSummaryJSON{
		CandidateID:   c.ID,
		State:         string(c.State),
		HeadSHA:       c.HeadSHA,
		PriorityScore: c.PriorityScore,
	}
	if c.ClusterID != "" {
		id := c.ClusterID
		out.ClusterID = &id
	}
	if c.RelationToRep != nil {
		r := string(*c.RelationToRep)
		out.RelationToRep = &r
	}
	return out
}
