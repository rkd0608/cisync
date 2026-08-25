package api

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
)

// runCascadeScenarios covers close/push/G10/unknown §3.1 rows against real
// Postgres; split from delivery_normalizer_pg_test.go for the line cap.
func runCascadeScenarios(t *testing.T, e *normalizerEnv) {
	t.Run("synchronize invalidates accepted evidence", func(t *testing.T) {
		intent, err := e.st.IntentForPR(migrateCtx(), e.tenant, e.repo, 7)
		if err != nil {
			t.Fatal(err)
		}
		// Seed one accepted evidence row against the LIVE candidate so the
		// revision chain must invalidate it (schema requires ≥1 ev_id).
		var candID string
		if err := e.st.Pool.QueryRow(migrateCtx(),
			`SELECT id FROM ctrl.candidates WHERE tenant_id=$1 AND intent_id=$2
			 AND head_sha='3333333333333333333333333333333333333333' AND state NOT IN ('superseded','cancelled')`,
			e.tenant, intent.ID).Scan(&candID); err != nil {
			t.Fatalf("live candidate for evidence seed: %v", err)
		}
		suffix := strings.ReplaceAll(e.guid(), "/", "-")
		if _, err := e.st.Pool.Exec(migrateCtx(),
			`INSERT INTO ctrl.evidence_records (tenant_id, id, seq, run_id, attempt, candidate_id,
			   kind, verdict, status, digests, inputs_hash, confidence, cost_millicents, created_at)
			 VALUES ($1,$2,0,$3,1,$4,'hermetic_build','pass','accepted','[]'::jsonb,'sha256:x',0.9,0,now())`,
			e.tenant, "ev_"+suffix, "run_"+suffix, candID); err != nil {
			t.Fatal(err)
		}
		beforeInvalidated := e.ledgerTypes(t)["evidence.invalidated"]
		newHead := strings.Replace(string(e.sync), "3333333333333333333333333333333333333333",
			"8888888888888888888888888888888888888888", 1)
		if code := postDelivery(t, e.ts, e.cfg.WebhookSecret, e.guid(), "pull_request.synchronize", e.repo, []byte(newHead)); code != http.StatusAccepted {
			t.Fatalf("synchronize = %d", code)
		}
		if after := e.ledgerTypes(t)["evidence.invalidated"]; after != beforeInvalidated+1 {
			t.Fatalf("evidence.invalidated delta = %d want 1", after-beforeInvalidated)
		}
	})

	t.Run("closed merged cancels live candidates", func(t *testing.T) {
		closed := loadFixture(t, "pull_request.closed.merged.json")
		beforeCancelled := e.ledgerTypes(t)["candidate.cancelled"]
		if code := postDelivery(t, e.ts, e.cfg.WebhookSecret, e.guid(), "pull_request.closed", e.repo, closed); code != http.StatusAccepted {
			t.Fatalf("closed delivery = %d", code)
		}
		if after := e.ledgerTypes(t)["candidate.cancelled"]; after != beforeCancelled+1 {
			t.Fatalf("candidate.cancelled delta = %d want 1", after-beforeCancelled)
		}
	})

	t.Run("push base advance supersedes stale-based candidates", func(t *testing.T) {
		push := loadFixture(t, "push.base_advanced.json")
		openedSecond := variantOpened(e.opened, 21)
		if code := postDelivery(t, e.ts, e.cfg.WebhookSecret, e.guid(), "pull_request.opened", e.repo, openedSecond); code != http.StatusAccepted {
			t.Fatalf("second opened = %d", code)
		}
		beforeMB := e.ledgerTypes(t)["merge_base.advanced"]
		if code := postDelivery(t, e.ts, e.cfg.WebhookSecret, e.guid(), "push", e.repo, push); code != http.StatusAccepted {
			t.Fatalf("push delivery = %d", code)
		}
		if after := e.ledgerTypes(t)["merge_base.advanced"]; after != beforeMB+1 {
			t.Fatal("tracked-base push must append merge_base.advanced")
		}

		branchPush := loadFixture(t, "push.branch.json")
		if code := postDelivery(t, e.ts, e.cfg.WebhookSecret, e.guid(), "push", e.repo, branchPush); code != http.StatusAccepted {
			t.Fatalf("branch push = %d", code)
		}
		if after := e.ledgerTypes(t)["merge_base.advanced"]; after != beforeMB+1 {
			t.Fatal("agent-branch push must stay record-only")
		}
	})

	t.Run("unknown gollum parks record-only", func(t *testing.T) {
		gollum := loadFixture(t, "gollum.unknown.json")
		code := postDelivery(t, e.ts, e.cfg.WebhookSecret, e.guid(), "gollum.edited", e.repo, gollum)
		if code != http.StatusAccepted {
			t.Fatalf("unknown events must never 4xx, got %d", code)
		}
	})

	t.Run("installation deleted revokes leases and suspends", func(t *testing.T) {
		deleted := loadFixture(t, "installation.deleted.json")
		repo2 := uniqueRepo()
		// The uninstall payload names REAL repos; retarget it at this run's
		// isolated repo so assertions stay scoped.
		deleted = []byte(strings.ReplaceAll(string(deleted),
			`"full_name": "acme/payments"`, fmt.Sprintf(`"full_name": %q`, repo2)))
		openedThird := variantOpened(e.opened, 9)
		if code := postDelivery(t, e.ts, e.cfg.WebhookSecret, e.guid(), "pull_request.opened", repo2, openedThird); code != http.StatusAccepted {
			t.Fatalf("pre-uninstall opened = %d", code)
		}
		intent, err := e.st.IntentForPR(migrateCtx(), e.tenant, repo2, 9)
		if err != nil {
			t.Fatal(err)
		}
		var leaseCount int
		if err := e.st.Pool.QueryRow(migrateCtx(),
			`SELECT count(*) FROM ctrl.leases WHERE tenant_id=$1 AND intent_id=$2 AND state IN ('requested','granted')`,
			e.tenant, intent.ID).Scan(&leaseCount); err != nil {
			t.Fatal(err)
		}
		if leaseCount == 0 {
			t.Fatal("synthetic intent must hold an active lease pre-uninstall")
		}
		beforeRevoked := e.ledgerTypes(t)["lease.revoked"]
		if code := postDelivery(t, e.ts, e.cfg.WebhookSecret, e.guid(), "installation.deleted", repo2, deleted); code != http.StatusAccepted {
			t.Fatalf("installation.deleted = %d", code)
		}
		if after := e.ledgerTypes(t)["lease.revoked"]; after != beforeRevoked+1 {
			t.Fatal("installation.deleted must revoke the repo's active lease")
		}
		if suspended, err := e.st.RepoSuspended(migrateCtx(), repo2); err != nil || !suspended {
			t.Fatalf("repo must be suspended: suspended=%v err=%v", suspended, err)
		}
		// Post-uninstall PR on the suspended repo creates NO new work (G10).
		openedFourth := variantOpened(e.opened, 10)
		if code := postDelivery(t, e.ts, e.cfg.WebhookSecret, e.guid(), "pull_request.opened", repo2, openedFourth); code != http.StatusAccepted {
			t.Fatalf("post-uninstall opened = %d", code)
		}
		if _, err := e.st.IntentForPR(migrateCtx(), e.tenant, repo2, 10); !errorsIsNotFound(err) {
			t.Fatal("suspended repo must not create synthetic intents")
		}
	})
}

// variantOpened renumbers a PR fixture so scenarios get fresh aggregates.
func variantOpened(opened []byte, number int) []byte {
	out := strings.Replace(string(opened), `"number": 7`, fmt.Sprintf(`"number": %d`, number), 3)
	return []byte(strings.ReplaceAll(out, "/pull/7.diff", fmt.Sprintf("/pull/%d.diff", number)))
}
