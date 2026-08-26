package api

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"

	"cisync.dev/cisync/control-plane/internal/audit"
	"cisync.dev/cisync/control-plane/internal/domain"
	"cisync.dev/cisync/control-plane/internal/store"
)

// applyPushBaseAdvanced implements §3.1 push.base_advanced: append
// merge_base.advanced and run the invalidation cascade over every live
// candidate still based on the old SHA. WHY supersede (not just evidence
// invalidation): a candidate whose base moved can never reach eligible —
// keeping it alive would strand queue slots; GitHub's own synchronize
// redelivers fresh heads that re-open work under new inputs (I-02).
func (s *Server) applyPushBaseAdvanced(ctx context.Context, tx pgx.Tx, tenant, extID string, view deliveryView) error {
	affected, err := s.store.LiveCandidatesByRepoBase(ctx, tenant, view.Repo, view.Push.OldSHA)
	if err != nil {
		return err
	}
	actor := webhookActor("")
	corr := domain.NewCorrelationID()
	affectedIDs := make([]string, 0, len(affected))
	for _, cand := range affected {
		affectedIDs = append(affectedIDs, cand.ID)
	}
	mbEvent, err := domain.NewEvent(tenant,
		domain.AggregateRef{Type: string(domain.AggDelivery), ID: domain.NewID(domain.PrefixDelivery)},
		"merge_base.advanced", extID, corr, actor, map[string]any{
			"repo":                   view.Repo,
			"old_sha":                view.Push.OldSHA,
			"new_sha":                view.Push.NewSHA,
			"affected_candidate_ids": anyStrings(affectedIDs),
			"branch":                 view.Push.Branch,
		})
	if err != nil {
		return err
	}
	events := []*domain.Event{mbEvent}
	if err := s.store.AppendEventsTx(ctx, tx, events); err != nil {
		return err
	}
	s.metrics.Add("cisync_ctrl_events_appended_total", float64(len(events)))
	for _, cand := range affected {
		// No successor exists yet at base-advance time; the self id marks
		// "no named replacement" until a fresh synchronize arrives.
		if err := s.supersedeCandidateTx(ctx, tx, tenant, extID, cand, cand.ID); err != nil {
			// A candidate already terminalized by a concurrent cascade is
			// logged-and-ignored (I-08), never a delivery failure.
			if errorsIsPostTerminal(err) {
				continue
			}
			return err
		}
	}
	s.metrics.Add("cisync_ctrl_merge_base_advances_total", 1)
	return nil
}

func errorsIsPostTerminal(err error) bool { return errors.Is(err, domain.ErrPostTerminal) }

// applyInstallationDeleted implements G10/§6.4: revoke every active lease on
// the installation's repos (existing revocation cascade kills queued runs),
// cancel live candidates, and suspend synthetic-intent creation for them.
func (s *Server) applyInstallationDeleted(ctx context.Context, tx pgx.Tx, tenant string, view deliveryView) error {
	if len(view.Install.Repos) == 0 {
		return nil
	}
	leases, err := s.store.ActiveLeasesForRepos(ctx, tenant, view.Install.Repos)
	if err != nil {
		return err
	}
	actor := webhookActor("")
	events := []*domain.Event{}
	runCancels := []string{}
	for _, lr := range leases {
		ev, err := domain.NewEvent(tenant,
			domain.AggregateRef{Type: string(domain.AggLease), ID: lr.LeaseID},
			"lease.revoked", "", domain.NewCorrelationID(), actor, map[string]any{
				"lease_id": lr.LeaseID,
				"reason":   "repo_deleted",
				"released_budget": map[string]any{
					"cpu_minutes":         lr.ReleasedBudget.CPUMinutes,
					"environment_minutes": lr.ReleasedBudget.EnvironmentMinutes,
					"repair_attempts":     lr.ReleasedBudget.RepairAttempts,
				},
			})
		if err != nil {
			return err
		}
		events = append(events, ev)
		if _, err := tx.Exec(ctx,
			`UPDATE ctrl.leases SET state='revoked', released_at=now() WHERE id=$1 AND state IN ('requested','granted')`,
			lr.LeaseID); err != nil {
			return err
		}
		// B7: teardown-class revocations are security-audit events, emitted
		// in the SAME tx as the cascade so the audit trail cannot lag the
		// ledger. repo_deleted is the live teardown reason (events.schema
		// lease.revoked enum); store.TeardownLeaseReasons documents why it
		// shares the tenant_teardown audit class.
		aev, aerr := audit.New(tenant, audit.KindLeaseRevocation,
			audit.Actor{Kind: "github", ID: "installation_cascade"},
			map[string]any{"lease_id": lr.LeaseID},
			map[string]any{"reason": "repo_deleted", "repos": view.Install.Repos})
		if aerr != nil {
			return aerr
		}
		if err := s.store.InsertSecurityAuditTx(ctx, tx, aev); err != nil {
			return err
		}
		s.metrics.Add("cisync_security_audit_total", 1, "kind", string(audit.KindLeaseRevocation))
		live, err := store.LiveCandidatesForIntentTx(ctx, tx, tenant, lr.IntentID)
		if err != nil {
			return err
		}
		for _, p := range live {
			cancelEv, err := domain.NewEvent(tenant,
				domain.AggregateRef{Type: string(domain.AggCandidate), ID: p.ID},
				"candidate.cancelled", "", domain.NewCorrelationID(), actor, map[string]any{
					"candidate_id": p.ID,
					"reason":       "intent_closed",
				})
			if err != nil {
				return err
			}
			events = append(events, cancelEv)
			queued, err := store.QueuedRunIDsTx(ctx, tx, tenant, p.ID)
			if err != nil {
				return err
			}
			for _, runID := range queued {
				rc, err := domain.NewEvent(tenant,
					domain.AggregateRef{Type: string(domain.AggRun), ID: runID},
					"validation.cancelled", "", domain.NewCorrelationID(), actor,
					map[string]any{"run_ids": []any{runID}, "reason": "intent_closed"})
				if err != nil {
					return err
				}
				events = append(events, rc)
				runCancels = append(runCancels, runID)
			}
			if err := store.CancelCandidateTx(ctx, tx, tenant, p.ID, cancelEv.Seq); err != nil {
				if errorsIsPostTerminal(err) {
					continue
				}
				return err
			}
		}
	}
	if err := s.appendEventsAndProjectCancels(ctx, tx, events, runCancels, nil); err != nil {
		return err
	}
	if err := store.SuspendReposTx(ctx, tx, view.Install.Repos); err != nil {
		return err
	}
	s.metrics.Add("cisync_ctrl_installation_suspensions_total", 1)
	return nil
}
