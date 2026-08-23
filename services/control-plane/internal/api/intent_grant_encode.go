package api

import (
	"sauron.dev/sauron/control-plane/internal/domain"
)

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
