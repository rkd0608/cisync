package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"cisync.dev/cisync/github-connector/internal/domain"
	"cisync.dev/cisync/github-connector/internal/rerun"
	"cisync.dev/cisync/github-connector/internal/tracking"
)

func validEnvelope() domain.DecisionEnvelope {
	return domain.DecisionEnvelope{
		Kind:        domain.KindDecision,
		DecisionID:  "dec_01JTESTDECISION",
		CandidateID: "cand_01JTESTCANDIDATE",
		Repo:        "acme/payments",
		HeadSHA:     "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Verb:        domain.VerbRejected,
		Confidence:  0.91,
		Policy:      domain.PolicyRef{PolicyID: "pol_cisync_default", Version: 1},
		Summary:     "deterministic regression",
		RenderedAt:  frozenNow,
		Evidence:    &domain.EvidenceCounts{Required: 5, Accepted: 5, Deferred: 2, Failed: 0},
	}
}

func TestDecisionsHMACEnforced(t *testing.T) {
	h := newHarness(t, rerun.PolicyReplan)
	raw, _ := json.Marshal(validEnvelope())
	req, _ := http.NewRequest(http.MethodPost, "/internal/connector/decisions", bytes.NewReader(raw))
	req.Header.Set("X-CISync-Signature", signBody([]byte("wrong"), raw))
	req.Header.Set("Idempotency-Key", "dec_01JTESTDECISION")
	rec := httptest.NewRecorder()
	h.handler.ServeHTTP(rec, req)
	require.Equal(t, http.StatusUnauthorized, rec.Code)

	_, err := h.tracker.FindByDecision(context.Background(), "dec_01JTESTDECISION")
	require.ErrorIs(t, err, tracking.ErrNotFound, "rejected pushes must not persist")
	require.Empty(t, h.router.creates, "rejected pushes must never publish")
}

func TestDecisionsDryRunAcceptsAndPersists(t *testing.T) {
	h := newHarness(t, rerun.PolicyReplan)
	env := validEnvelope()

	resp := h.post(t, env, env.DecisionID)
	require.Equal(t, http.StatusAccepted, resp.Code)
	var body pushResponse
	require.NoError(t, json.Unmarshal(resp.Body.Bytes(), &body))
	require.True(t, body.Accepted)
	require.True(t, body.DryRun)
	require.Len(t, h.router.creates, 1)
	require.Equal(t, env.CandidateID, h.router.creates[0].ExternalID, "B1/G6 external_id")

	rep, err := h.tracker.FindByDecision(context.Background(), env.DecisionID)
	require.NoError(t, err)
	require.Equal(t, "failure", rep.Conclusion)

	replay := h.post(t, env, env.DecisionID)
	require.Equal(t, http.StatusOK, replay.Code, "replay is idempotent 200")
	require.Len(t, h.router.creates, 1, "replay never republishes")
}

func TestDecisionsUpdateInPlaceForTrackedRevision(t *testing.T) {
	h := newHarness(t, rerun.PolicyReplan)
	ctx := context.Background()

	// Lifecycle queued first (creates run id 1), then decision updates it.
	lc := domain.LifecycleEnvelope{
		Kind: domain.KindLifecycle, Phase: domain.LifecycleQueued,
		CandidateID: "cand_01JTESTCANDIDATE", Repo: "acme/payments",
		HeadSHA: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", At: frozenNow,
	}
	resp := h.post(t, lc, lc.CandidateID+":queued")
	require.Equal(t, http.StatusAccepted, resp.Code)
	require.Len(t, h.router.creates, 1)

	resp = h.post(t, validEnvelope(), validEnvelope().DecisionID)
	require.Equal(t, http.StatusAccepted, resp.Code)
	require.Len(t, h.router.creates, 1, "no NEW create: decision updates the existing run")
	require.Len(t, h.router.updates, 1)
	require.Equal(t, int64(1), h.router.updateIDs[0], "one check run per revision")

	rec, err := h.tracker.LookupCheckReport(ctx, "cand_01JTESTCANDIDATE", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	require.NoError(t, err)
	require.Equal(t, domain.PhaseCompleted, rec.Phase)
	require.NotNil(t, rec.LastDecision, "decision stored for replay_cached")
}

func TestDecisionsValidationFailures(t *testing.T) {
	h := newHarness(t, rerun.PolicyReplan)

	bad := validEnvelope()
	bad.Verb = "combine"
	require.Equal(t, http.StatusBadRequest, h.post(t, bad, bad.DecisionID).Code)

	bad = validEnvelope()
	bad.HeadSHA = "short"
	require.Equal(t, http.StatusBadRequest, h.post(t, bad, bad.DecisionID).Code)

	bad = validEnvelope()
	bad.Evidence = &domain.EvidenceCounts{Required: 2, Accepted: 5}
	require.Equal(t, http.StatusBadRequest, h.post(t, bad, bad.DecisionID).Code, "inconsistent evidence fails closed")

	mismatched := validEnvelope()
	require.Equal(t, http.StatusBadRequest, h.post(t, mismatched, "dec_other").Code,
		"Idempotency-Key must equal decision_id")
}

func TestDecisionsPublishFailureIs502WithoutPersisting(t *testing.T) {
	h := newHarness(t, rerun.PolicyReplan)
	h.router.err = errors.New("github down")
	env := validEnvelope()
	resp := h.post(t, env, env.DecisionID)
	require.Equal(t, http.StatusBadGateway, resp.Code)
	_, err := h.tracker.FindByDecision(context.Background(), env.DecisionID)
	require.ErrorIs(t, err, tracking.ErrNotFound, "failed publications must not record")
}
