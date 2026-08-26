package api

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"cisync.dev/cisync/control-plane/internal/domain"
	"cisync.dev/cisync/control-plane/internal/store"
)

// nowUTC pins effect timestamps to UTC for deterministic serialization.
func nowUTC() time.Time { return time.Now().UTC() }

// webhookActor is the ledger actor for every synthetic webhook effect.
func webhookActor(login string) domain.EventActor {
	if login == "" {
		login = "github"
	}
	return domain.EventActor{Kind: string(domain.ActorGitHub), ID: login}
}

func errorsIsNotFound(err error) bool { return errors.Is(err, domain.ErrNotFound) }

// applyPROpened implements §3.1 pr.opened: find-or-create the synthetic
// intent by (repo, pr_number) then submit the candidate idempotently.
// Suspended repos (G10) record-only — zero effects after uninstall.
func (s *Server) applyPROpened(ctx context.Context, tx pgx.Tx, tenant string, view deliveryView) error {
	suspended, err := s.store.RepoSuspended(ctx, view.Repo)
	if err != nil {
		return err
	}
	if suspended {
		s.metrics.Add("cisync_ctrl_webhook_suspended_skips_total", 1)
		return nil
	}
	intent, err := s.store.IntentForPR(ctx, tenant, view.Repo, view.PR.Number)
	if errorsIsNotFound(err) {
		if intent, err = s.createSyntheticIntentTx(ctx, tx, tenant, view); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	_, err = s.submitWebhookCandidateTx(ctx, tx, intent, tenant, view)
	return err
}

// applyPRSynchronize implements §3.3: same-head redelivery is a replay,
// stale heads are record-only diagnostics, fresh heads open a revision chain
// that supersedes the prior candidate and invalidates its evidence.
func (s *Server) applyPRSynchronize(ctx context.Context, tx pgx.Tx, tenant, extID string, view deliveryView) error {
	intent, err := s.store.IntentForPR(ctx, tenant, view.Repo, view.PR.Number)
	if errorsIsNotFound(err) {
		// Unknown PR: nothing to revise; the delivery stays recorded+acked.
		s.metrics.Add("cisync_ctrl_webhook_orphans_total", 1)
		return nil
	}
	if err != nil {
		return err
	}
	live, known, err := store.CandidateHeadStateTx(ctx, tx, tenant, intent.ID, view.PR.HeadSHA)
	if err != nil {
		return err
	}
	switch {
	case known && live:
		// Same-head redelivery: duplicate_sha semantics ⇒ 200 replay.
		s.metrics.Add("cisync_ctrl_webhook_replays_total", 1)
		return nil
	case known:
		// Out-of-order guard: this head was already superseded/cancelled, so
		// a stale redelivery must never supersede newer work (plan §3.2).
		s.metrics.Add("cisync_ctrl_webhook_stale_heads_total", 1)
		return nil
	}
	prior, err := store.LiveCandidatesForIntentTx(ctx, tx, tenant, intent.ID)
	if err != nil {
		return err
	}
	successor, err := s.submitWebhookCandidateTx(ctx, tx, intent, tenant, view)
	if err != nil {
		return err
	}
	for _, p := range prior {
		if p.ID == successor.ID {
			continue
		}
		if err := s.supersedeCandidateTx(ctx, tx, tenant, extID, p, successor.ID); err != nil {
			return err
		}
	}
	return nil
}

// supersedeCandidateTx appends the revision-chain events for one prior
// candidate and flips its projection row. Causation = the synchronize
// delivery id so dossiers can show WHY evidence died (plan §3.3).
func (s *Server) supersedeCandidateTx(ctx context.Context, tx pgx.Tx, tenant, extDeliveryID string, prior *domain.Candidate, successorID string) error {
	actor := webhookActor("")
	corr := domain.NewCorrelationID()
	ev, err := domain.NewEvent(tenant,
		domain.AggregateRef{Type: string(domain.AggCandidate), ID: prior.ID},
		"candidate.superseded", extDeliveryID, corr, actor, map[string]any{
			"candidate_id":    prior.ID,
			"by_candidate_id": successorID,
			"reason":          "head_advanced",
		})
	if err != nil {
		return err
	}
	runIDs, err := store.QueuedRunIDsTx(ctx, tx, tenant, prior.ID)
	if err != nil {
		return err
	}
	cancelEvents, err := cancelQueuedRunsEvents(tenant, runIDs, "superseded", actor)
	if err != nil {
		return err
	}
	evIDs, err := store.AcceptedEvidenceIDsTx(ctx, tx, tenant, prior.ID)
	if err != nil {
		return err
	}
	events := append([]*domain.Event{ev}, cancelEvents...)
	if len(evIDs) > 0 {
		inv, err := domain.NewEvent(tenant,
			domain.AggregateRef{Type: string(domain.AggEvidence), ID: evIDs[0]},
			"evidence.invalidated", extDeliveryID, corr, actor, map[string]any{
				"ev_ids": anyStrings(evIDs),
				"reason": "base_advanced",
			})
		if err != nil {
			return err
		}
		events = append(events, inv)
	}
	repairIDs, err := store.ActiveRepairTaskIDsTx(ctx, tx, tenant, prior.ID)
	if err != nil {
		return err
	}
	for _, repairID := range repairIDs {
		abort, aerr := domain.NewEvent(tenant,
			domain.AggregateRef{Type: string(domain.AggRepairTask), ID: repairID},
			"repair.completed", extDeliveryID, corr, actor, map[string]any{
				"repair_id":     repairID,
				"outcome":       "aborted",
				"attempts_used": 0,
			})
		if aerr != nil {
			return aerr
		}
		events = append(events, abort)
	}
	if err := s.appendEventsAndProjectCancels(ctx, tx, events, runIDs, repairIDs); err != nil {
		return err
	}
	return store.SupersedeCandidateTx(ctx, tx, tenant, prior.ID, ev.Seq)
}
