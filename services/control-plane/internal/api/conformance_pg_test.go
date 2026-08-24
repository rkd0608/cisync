package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"testing"

	"github.com/jackc/pgx/v5"

	"sauron.dev/sauron/control-plane/internal/domain"
	"sauron.dev/sauron/control-plane/internal/store"
)

// Wave-3 conformance regressions: each test pins a frozen-contract behavior
// that drifted in the running system. All are PG-backed; skip without
// TEST_PG_DSN so hermetic runs stay green.

// TestReplayBodyByteIdentical pins openapi's "idempotent replay returns the
// original response": byte-identical, not merely semantically equal — jsonb
// round-trips renormalize key order/spacing and broke I-12.
func TestReplayBodyByteIdentical(t *testing.T) {
	ts, st, cfg := pgServer(t)
	defer ts.Close()
	defer st.Close()

	headers := map[string]string{"Idempotency-Key": idemKey("replay")}
	body := map[string]any{
		"goal": "byte identity probe", "repository": fmt.Sprintf("acme/replay-%d", os.Getpid()),
		"base": "main", "expected_surfaces": []string{"services/**"}, "risk": "low",
	}
	first, c1 := authedJSON(ts, http.MethodPost, "/v1/change-intents", cfg.AdminToken, body, headers)
	rawFirst := readAll(t, first.Body)
	c1()
	if first.StatusCode != http.StatusOK {
		t.Fatalf("first create = %d", first.StatusCode)
	}
	second, c2 := authedJSON(ts, http.MethodPost, "/v1/change-intents", cfg.AdminToken, body, headers)
	rawSecond := readAll(t, second.Body)
	c2()
	if second.StatusCode != first.StatusCode {
		t.Fatalf("replay status %d != original %d", second.StatusCode, first.StatusCode)
	}
	if !bytes.Equal(rawFirst, rawSecond) {
		t.Fatalf("replay body not byte-identical:\nfirst:  %s\nsecond: %s", rawFirst, rawSecond)
	}
}

// TestRenewAfterTerminalConflictShape pins the openapi Conflict envelope for
// post-terminal lease renewal: 409 conflict_state with details.reason from
// {expired_lease, revoked_lease} — never a bare 503.
func TestRenewAfterTerminalConflictShape(t *testing.T) {
	ts, st, cfg := pgServer(t)
	defer ts.Close()
	defer st.Close()

	_, leaseID := createReleasedIntentLease(t, ts, cfg)

	renewBody := map[string]any{"ttl_seconds": 1800}
	resp, close1 := authedJSON(ts, http.MethodPost, "/v1/leases/"+leaseID+"/renew", cfg.AdminToken, renewBody,
		map[string]string{"Idempotency-Key": idemKey("renew")})
	defer close1()
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("renew after release = %d, want 409", resp.StatusCode)
	}
	var envelope ErrorEnvelope
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Error.Code != "conflict_state" {
		t.Fatalf("code=%s want conflict_state", envelope.Error.Code)
	}
	reason, _ := envelope.Error.Details["reason"].(string)
	if reason != "expired_lease" && reason != "revoked_lease" {
		t.Fatalf("details.reason=%q must be expired_lease|revoked_lease", reason)
	}
}

// TestSameHeadDifferentBaseDistinctPlans pins I-02 end-to-end: an identical
// patch under a different base_sha is NOT duplicate_sha; it plans fresh with
// a distinct inputs_hash (changed input ⇒ cache miss).
func TestSameHeadDifferentBaseDistinctPlans(t *testing.T) {
	ts, st, cfg := pgServer(t)
	defer ts.Close()
	defer st.Close()

	repo := fmt.Sprintf("acme/base-move-%d-%s", os.Getpid(), uniqueSuffix())
	createHeaders := map[string]string{"Idempotency-Key": idemKey("bm-ci")}
	body := map[string]any{
		"goal": "g", "repository": repo, "base": "main",
		"expected_surfaces": []string{"services/**"}, "risk": "medium",
	}
	resp, c1 := authedJSON(ts, http.MethodPost, "/v1/change-intents", cfg.AdminToken, body, createHeaders)
	var grant map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&grant)
	c1()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("create = %d", resp.StatusCode)
	}
	intentID := grant["intent_id"].(string)

	const headSHA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	first := submitCandidateRaw(t, ts, cfg.AdminToken, intentID, headSHA, deterministicSHA(2), "b1")
	if first.status != http.StatusCreated {
		t.Fatalf("first submit = %d: %s", first.status, first.raw)
	}
	second := submitCandidateRaw(t, ts, cfg.AdminToken, intentID, headSHA, deterministicSHA(3), "b2")
	if second.status != http.StatusCreated {
		t.Fatalf("same head under different base must plan fresh: got %d: %s", second.status, second.raw)
	}

	h1 := plannedInputsHash(st, cfg, stringFromJSON(t, first.raw, "candidate_id"))
	h2 := plannedInputsHash(st, cfg, stringFromJSON(t, second.raw, "candidate_id"))
	if h1 == "" || h2 == "" {
		t.Fatalf("both candidates need planned inputs_hash: %q vs %q", h1, h2)
	}
	if h1 == h2 {
		t.Fatalf("different base_sha must yield distinct inputs_hash, both %q", h1)
	}
}

// TestIntentReservesFirstCandidateSlot pins the §3b walkthrough trace: the
// intent declaration reserves the FIRST candidate slot so the whole documented
// lifecycle (intent.declared → … → decision.rendered) is one continuous,
// publicly-observable sequence keyed by a single candidate id.
func TestIntentReservesFirstCandidateSlot(t *testing.T) {
	ts, st, cfg := pgServer(t)
	defer ts.Close()
	defer st.Close()

	repo := fmt.Sprintf("acme/slot-%d-%s", os.Getpid(), uniqueSuffix())
	grantRaw := createIntentRaw(t, ts, cfg.AdminToken, repo)
	intentID := stringFromJSON(t, grantRaw, "intent_id")

	ctx := context.Background()
	var reserved string
	if err := st.Pool.QueryRow(ctx,
		`SELECT initial_candidate_id FROM ctrl.intents WHERE id=$1 AND tenant_id=$2`,
		intentID, cfg.TenantID).Scan(&reserved); err != nil {
		t.Fatalf("read reserved slot: %v", err)
	}
	if len(reserved) != len("cand_")+26 {
		t.Fatalf("reserved slot must be a cand_ ULID, got %q", reserved)
	}

	first := submitCandidateRaw(t, ts, cfg.AdminToken, intentID, deterministicSHA(7), deterministicSHA(8), "s1")
	if first.status != http.StatusCreated {
		t.Fatalf("first submit = %d: %s", first.status, first.raw)
	}
	if got := stringFromJSON(t, first.raw, "candidate_id"); got != reserved {
		t.Fatalf("first submission must consume the reserved slot: got %q want %q", got, reserved)
	}

	second := submitCandidateRaw(t, ts, cfg.AdminToken, intentID, deterministicSHA(9), deterministicSHA(8), "s2")
	if second.status != http.StatusCreated {
		t.Fatalf("second submit = %d: %s", second.status, second.raw)
	}
	if got := stringFromJSON(t, second.raw, "candidate_id"); got == reserved {
		t.Fatal("subsequent submissions must mint fresh ids, not reuse the slot")
	}
}

// TestRunLifecycleEventsCarryCandidateCorrelation pins the served-lifecycle
// walkthrough (ARCHITECTURE_DRAFT §3a): validation.started /
// validation.completed stamp candidate_id so one candidate's documented event
// sequence is observable through the public tail.
func TestRunLifecycleEventsCarryCandidateCorrelation(t *testing.T) {
	ts, st, cfg := pgServer(t)
	defer ts.Close()
	defer st.Close()

	repo := fmt.Sprintf("acme/correlation-%d-%s", os.Getpid(), uniqueSuffix())
	grant := createIntentRaw(t, ts, cfg.AdminToken, repo)
	cand := submitCandidateRaw(t, ts, cfg.AdminToken, stringFromJSON(t, grant, "intent_id"),
		deterministicSHA(5), deterministicSHA(6), "corr")
	if cand.status != http.StatusCreated {
		t.Fatalf("submit = %d: %s", cand.status, cand.raw)
	}
	candidateID := stringFromJSON(t, cand.raw, "candidate_id")

	// The submit path appends validation.started only when the scheduler
	// dispatches; here we drive the store transition directly to pin the
	// payload contract at unit level.
	ctx := context.Background()
	var runID string
	if err := st.Pool.QueryRow(ctx,
		`SELECT id FROM ctrl.validation_runs WHERE tenant_id=$1 AND candidate_id=$2 LIMIT 1`,
		cfg.TenantID, candidateID).Scan(&runID); err != nil {
		t.Fatalf("queued run lookup: %v", err)
	}
	run, err := st.GetRunByID(ctx, runID)
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	var started *domain.Event
	if err := st.ExecTx(ctx, func(tx pgx.Tx) error {
		ev, err := store.DispatchRunTx(ctx, tx, st, run)
		if err != nil {
			return err
		}
		started = ev
		return nil
	}); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if got, _ := started.Payload["candidate_id"].(string); got != candidateID {
		t.Fatalf("validation.started must carry candidate_id=%q, got %v", candidateID, started.Payload["candidate_id"])
	}
}
