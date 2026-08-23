package scheduler

import (
	"testing"

	"github.com/stretchr/testify/require"
	"pgregory.net/rapid"
)

// TestPropertyAdmissionNeverExceedsCapsOrBudgets is the I-06/I-10 core: for
// arbitrary loads, every admitted run fit ALL its dimensions atomically and
// the deltas exactly equal an independent recount of admitted reservations.
func TestPropertyAdmissionNeverExceedsCapsOrBudgets(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		batch := genBatch(t)
		capsG := genCaps(t)
		wip := genWIP(t)
		budgetsG := genBudgets(t)

		res := Admit(batch, capsG, wip, budgetsG)

		cpuUsed := map[string]int64{}
		concUsed := map[string]int64{}
		wipAdded := map[int]int{}
		admittedCands := map[string]map[string]struct{}{}

		require.Len(t, res.Admissions, len(batch))
		for _, a := range res.Admissions {
			var run Run
			found := false
			for _, rr := range batch {
				if rr.Run.ID == a.RunID {
					run = rr.Run
					found = true
					break
				}
			}
			require.True(t, found)
			if !a.Admitted {
				require.NotEmpty(t, a.DenyReason, "denials carry a machine reason")
				continue
			}
			need := cpuMinutes(run.EstDurationMS)
			concNeed := int64(0)
			if admittedCands[run.TenantID] == nil {
				admittedCands[run.TenantID] = map[string]struct{}{}
			}
			if _, dup := admittedCands[run.TenantID][run.CandidateID]; !dup {
				concNeed = 1
			}

			capTier, capped := capOf(capsG, run.Tier)
			require.True(t, capped, "admitted on unconfigured tier")
			require.LessOrEqual(t, wip.InFlightByTier[run.Tier]+wipAdded[run.Tier]+1, capTier,
				"WIP cap overrun")
			require.GreaterOrEqual(t, budgetsG.TenantCPURemaining[run.TenantID]-cpuUsed[run.TenantID], need,
				"cpu budget overrun")
			require.GreaterOrEqual(t, budgetsG.TenantConcurrentRemaining[run.TenantID]-concUsed[run.TenantID], concNeed,
				"concurrency cap overrun")

			if need > 0 {
				cpuUsed[run.TenantID] += need
			}
			if concNeed > 0 {
				concUsed[run.TenantID] += concNeed
			}
			wipAdded[run.Tier]++
			admittedCands[run.TenantID][run.CandidateID] = struct{}{}
		}

		for tenant, used := range cpuUsed {
			require.Equal(t, used, res.Deltas.CPUMinutesByTenant[tenant], "conservation cpu "+tenant)
		}
		require.Len(t, res.Deltas.CPUMinutesByTenant, len(cpuUsed))
		for tenant, used := range concUsed {
			require.Equal(t, used, res.Deltas.ConcurrentByTenant[tenant], "conservation conc "+tenant)
		}
		require.Len(t, res.Deltas.ConcurrentByTenant, len(concUsed))
		for tier, added := range wipAdded {
			require.Equal(t, added, res.Deltas.WIPAddedByTier[tier])
		}
	})
}

// TestPropertyAdmitDeterministicUnderPermutation: identical inputs in any
// input order produce the identical admission sequence (pure function).
func TestPropertyAdmitDeterministicUnderPermutation(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		batch := genBatch(t)
		capsG := genCaps(t)
		wip := genWIP(t)
		budgetsG := genBudgets(t)

		reversed := make([]RankedRun, len(batch))
		for i, rr := range batch {
			reversed[len(batch)-1-i] = rr
		}
		a := Admit(batch, capsG, wip, budgetsG)
		b := Admit(reversed, capsG, wip, budgetsG)
		require.Equal(t, a, b)
	})
}
