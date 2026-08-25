package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"sauron.dev/sauron/github-connector/internal/domain"
	"sauron.dev/sauron/github-connector/internal/rerun"
	"sauron.dev/sauron/github-connector/internal/tracking"
)

// acceptedCtrl is the frozen revalidate contract: 202 {"accepted":true}.
func acceptedCtrl(hits *int) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*hits++
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"accepted":true}`))
	}))
}

// conflictCtrl answers 409 rerun_budget_exhausted (ctrl cap or terminal state).
func conflictCtrl(hits *int) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*hits++
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"error":{"code":"conflict_state","details":{"reason":"rerun_budget_exhausted"}}}`))
	}))
}

func cachedDecision() *domain.DecisionEnvelope {
	return &domain.DecisionEnvelope{
		Kind: domain.KindDecision, DecisionID: "dec_01JCACHED", CandidateID: "cand_01JRERUN",
		Repo: "acme/payments", HeadSHA: "cccccccccccccccccccccccccccccccccccccccc",
		Verb: domain.VerbEligibleForMergeTrain, Confidence: 0.9,
		Policy:     domain.PolicyRef{PolicyID: "pol_sauron_default", Version: 1},
		RenderedAt: frozenNow,
		Evidence:   &domain.EvidenceCounts{Required: 4, Accepted: 4, Deferred: 0, Failed: 0},
	}
}

func seedRevision(t *testing.T, h *harness) {
	t.Helper()
	require.NoError(t, h.tracker.RecordCheckReport(context.Background(), tracking.Record{
		CandidateID: "cand_01JRERUN", HeadSHA: "cccccccccccccccccccccccccccccccccccccccc",
		Repo: "acme/payments", CheckRunID: 55, Phase: domain.PhaseCompleted,
		Conclusion: "success", DecisionID: "dec_01JCACHED", LastDecision: cachedDecision(),
	}))
}

func rerunEnvelope() domain.RerunEnvelope {
	return domain.RerunEnvelope{
		Kind: domain.KindRerun, CandidateID: "cand_01JRERUN", Repo: "acme/payments",
		HeadSHA: "cccccccccccccccccccccccccccccccccccccccc", RequestedAt: frozenNow,
	}
}

func decodeBody(t *testing.T, rec *httptest.ResponseRecorder) pushResponse {
	t.Helper()
	var body pushResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	return body
}

func TestRerunReplanCallsCtrlAndFlipsToQueued(t *testing.T) {
	var lastIDEM string
	hits := 0
	ctrl := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		lastIDEM = r.Header.Get("Idempotency-Key")
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"accepted":true}`))
	}))
	defer ctrl.Close()

	h := newHarness(t, rerun.PolicyReplan)
	h.withControl(rerun.NewControl(ctrl.URL, "tok", ctrl.Client(), func() time.Time { return frozenNow }))
	seedRevision(t, h)

	resp := h.post(t, rerunEnvelope(), "d-000001-00000000-0000-4821-9f10-000000000001")
	require.Equal(t, http.StatusAccepted, resp.Code)
	require.Equal(t, 1, hits, "replan POSTs the revalidate command")
	require.Equal(t, "d-000001-00000000-0000-4821-9f10-000000000001", lastIDEM,
		"originating ext_delivery_id rides as the revalidate Idempotency-Key (ctrl requires 16..128 chars)")
	require.Len(t, h.router.updates, 1)
	require.Equal(t, "queued", h.router.updates[0].Status, "check flips back to queued")
	rec, err := h.tracker.LookupCheckReport(context.Background(),
		"cand_01JRERUN", "cccccccccccccccccccccccccccccccccccccccc")
	require.NoError(t, err)
	require.Equal(t, domain.PhaseQueued, rec.Phase)
}

func TestRerunReplayCachedRepublishesStoredDecision(t *testing.T) {
	h := newHarness(t, rerun.PolicyReplayCached)
	seedRevision(t, h)

	resp := h.post(t, rerunEnvelope(), "d-000002-00000000-0000-4821-9f10-000000000002")
	require.Equal(t, http.StatusAccepted, resp.Code)
	body := decodeBody(t, resp)
	require.Equal(t, "replayed_cached", body.Outcome)
	require.Len(t, h.router.updates, 1)
	require.Contains(t, h.router.updates[0].Summary, "cached replay (no recompute)")
	require.Equal(t, "success", h.router.updates[0].Conclusion)
}

func TestRerunOverCapFlipsNeutralExhausted(t *testing.T) {
	hits := 0
	ctrl := acceptedCtrl(&hits)
	defer ctrl.Close()

	h := newHarness(t, rerun.PolicyReplan)
	h.withControl(rerun.NewControl(ctrl.URL, "tok", ctrl.Client(), func() time.Time { return frozenNow }))
	seedRevision(t, h)

	// Burn both per-candidate slots (frozen ruling §10.2: cap = 2).
	h.budget.Record("cand_01JRERUN", 42)
	h.budget.Record("cand_01JRERUN", 42)

	resp := h.post(t, rerunEnvelope(), "d-000003-00000000-0000-4821-9f10-000000000003")
	require.Equal(t, http.StatusAccepted, resp.Code)
	body := decodeBody(t, resp)
	require.Equal(t, "exhausted", body.Outcome)
	require.Zero(t, hits, "over-cap never reaches control-plane")
	require.Len(t, h.router.updates, 1)
	require.Equal(t, "neutral", h.router.updates[0].Conclusion)
	require.Contains(t, h.router.updates[0].Summary, "**Re-run budget exhausted**")
}

func TestRerunUnreachableCtrlDeclinesNeutralNotSilent(t *testing.T) {
	h := newHarness(t, rerun.PolicyReplan) // no ctrl wired ⇒ flag off
	seedRevision(t, h)

	resp := h.post(t, rerunEnvelope(), "d-000004-00000000-0000-4821-9f10-000000000004")
	require.Equal(t, http.StatusAccepted, resp.Code)
	body := decodeBody(t, resp)
	require.Equal(t, "unavailable", body.Outcome)
	require.Len(t, h.router.updates, 1)
	require.Contains(t, h.router.updates[0].Summary, "**Re-run unavailable**")
	require.Empty(t, h.router.creates, "no stray publications")
}

func TestRerunUnknownCandidate404AndDuplicateReplays(t *testing.T) {
	h := newHarness(t, rerun.PolicyReplan)
	resp := h.post(t, rerunEnvelope(), "d-000005-00000000-0000-4821-9f10-000000000005")
	require.Equal(t, http.StatusNotFound, resp.Code, "untracked candidate ⇒ typed 404")

	h2 := newHarness(t, rerun.PolicyReplan)
	seedRevision(t, h2)
	require.Equal(t, http.StatusAccepted, h2.post(t, rerunEnvelope(), "d-000006-00000000-0000-4821-9f10-000000000006").Code)
	replay := h2.post(t, rerunEnvelope(), "d-000006-00000000-0000-4821-9f10-000000000006")
	require.Equal(t, http.StatusOK, replay.Code, "same delivery id ⇒ replay")
	require.Len(t, h2.router.updates, 1, "duplicate burns no budget")
}

func TestRerunCtrlBudgetConflictDeclinesNeutralExhausted(t *testing.T) {
	hits := 0
	ctrl := conflictCtrl(&hits)
	defer ctrl.Close()

	h := newHarness(t, rerun.PolicyReplan)
	h.withControl(rerun.NewControl(ctrl.URL, "tok", ctrl.Client(), func() time.Time { return frozenNow }))
	seedRevision(t, h)

	resp := h.post(t, rerunEnvelope(), "d-000007-00000000-0000-4821-9f10-000000000007")
	require.Equal(t, http.StatusAccepted, resp.Code)
	body := decodeBody(t, resp)
	require.Equal(t, "exhausted", body.Outcome,
		"ctrl 409 budget exhaustion is an exhausted decline, not a transient unavailable")
	require.Len(t, h.router.updates, 1)
	require.Equal(t, "neutral", h.router.updates[0].Conclusion)
	require.Contains(t, h.router.updates[0].Summary, "**Re-run budget exhausted**")
}
