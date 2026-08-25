package api

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"sauron.dev/sauron/github-connector/internal/domain"
	"sauron.dev/sauron/github-connector/internal/rerun"
	"sauron.dev/sauron/github-connector/internal/tracking"
)

func lifecycleEnvelope(phase domain.LifecyclePhase) domain.LifecycleEnvelope {
	return domain.LifecycleEnvelope{
		Kind: domain.KindLifecycle, Phase: phase,
		CandidateID: "cand_01JLIFE", Repo: "acme/payments",
		HeadSHA: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", At: frozenNow,
	}
}

func TestLifecycleWalksQueuedThenInProgressOnSameRun(t *testing.T) {
	h := newHarness(t, rerun.PolicyReplan)
	ctx := context.Background()

	resp := h.post(t, lifecycleEnvelope(domain.LifecycleQueued), "cand_01JLIFE:queued")
	require.Equal(t, http.StatusAccepted, resp.Code)
	var body pushResponse
	require.NoError(t, json.Unmarshal(resp.Body.Bytes(), &body))
	require.True(t, body.DryRun)
	require.Len(t, h.router.creates, 1)
	require.Equal(t, "queued", h.router.creates[0].Status)
	require.Empty(t, h.router.creates[0].Conclusion)

	resp = h.post(t, lifecycleEnvelope(domain.LifecycleInProgress), "cand_01JLIFE:in_progress")
	require.Equal(t, http.StatusAccepted, resp.Code)
	require.Len(t, h.router.creates, 1, "in_progress UPDATES the same run")
	require.Len(t, h.router.updates, 1)
	require.Equal(t, int64(1), h.router.updateIDs[0], "B2/G7 update-in-place")
	require.Equal(t, "in_progress", h.router.updates[0].Status)

	rec, err := h.tracker.LookupCheckReport(ctx, "cand_01JLIFE", "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")
	require.NoError(t, err)
	require.Equal(t, domain.PhaseInProgress, rec.Phase)
}

func TestLifecycleReplaysAndIgnoresLatePhases(t *testing.T) {
	h := newHarness(t, rerun.PolicyReplan)

	// Same-phase redelivery is a replay (deterministic Idempotency-Key).
	require.Equal(t, http.StatusAccepted, h.post(t, lifecycleEnvelope(domain.LifecycleQueued), "cand_01JLIFE:queued").Code)
	replay := h.post(t, lifecycleEnvelope(domain.LifecycleQueued), "cand_01JLIFE:queued")
	require.Equal(t, http.StatusOK, replay.Code)
	var body pushResponse
	require.NoError(t, json.Unmarshal(replay.Body.Bytes(), &body))
	require.True(t, body.Replay)
	require.Len(t, h.router.creates, 1)

	// Terminal revisions ignore late lifecycle pushes.
	require.NoError(t, h.tracker.RecordCheckReport(context.Background(), tracking.Record{
		CandidateID: "cand_01JLIFE", HeadSHA: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		CheckRunID: 5, Phase: domain.PhaseCompleted, Conclusion: "success",
	}))
	late := h.post(t, lifecycleEnvelope(domain.LifecycleInProgress), "cand_01JLIFE:in_progress")
	require.Equal(t, http.StatusOK, late.Code)
	require.Empty(t, h.router.updates, "late phase push after completion must not touch GitHub")
}

func TestLifecycleValidationAndIdempotencyKey(t *testing.T) {
	h := newHarness(t, rerun.PolicyReplan)

	bad := lifecycleEnvelope("completed") // completed never travels the wire
	resp := h.post(t, bad, "cand_01JLIFE:completed")
	require.Equal(t, http.StatusBadRequest, resp.Code)

	wrongKey := lifecycleEnvelope(domain.LifecycleQueued)
	require.Equal(t, http.StatusBadRequest, h.post(t, wrongKey, "cand_01JLIFE:in_progress").Code)

	badSHA := lifecycleEnvelope(domain.LifecycleQueued)
	badSHA.HeadSHA = "nothex"
	require.Equal(t, http.StatusBadRequest, h.post(t, badSHA, "cand_01JLIFE:queued").Code)
}

// TestSection4EnvelopeDecodeConformance pins the exact §4 wire shapes:
// every documented field name survives decoding into the typed envelopes.
func TestSection4EnvelopeDecodeConformance(t *testing.T) {
	decisionJSON := `{
		"kind":"decision","decision_id":"dec_01J","candidate_id":"cand_01J",
		"repo":"acme/payments","head_sha":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		"verb":"rejected","confidence":0.91,
		"policy":{"policy_id":"pol_x","policy_version":4},
		"summary":"s","rendered_at":"2026-08-23T03:41:00Z",
		"evidence":{"required":5,"accepted":4,"deferred":1,"failed":0},
		"annotations":[{"path":"p/a.go","start_line":7,"message":"m","kind":"api_compat"}]
	}`
	var d domain.DecisionEnvelope
	require.NoError(t, json.Unmarshal([]byte(decisionJSON), &d))
	require.Equal(t, domain.KindDecision, d.Kind)
	require.Equal(t, 4, d.Evidence.Accepted)
	require.Equal(t, 7, d.Annotations[0].StartLine)
	require.NoError(t, d.Validate())

	lifecycleJSON := `{"kind":"lifecycle","phase":"queued","candidate_id":"cand_01J",
		"repo":"acme/payments","head_sha":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		"at":"2026-08-23T03:41:00Z"}`
	var l domain.LifecycleEnvelope
	require.NoError(t, json.Unmarshal([]byte(lifecycleJSON), &l))
	require.Equal(t, domain.LifecycleQueued, l.Phase)
	require.NoError(t, l.Validate())

	rerunJSON := `{"kind":"rerun_requested","candidate_id":"cand_01J",
		"repo":"acme/payments","head_sha":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		"requested_by":"github:octocat","requested_at":"2026-08-23T03:41:00Z"}`
	var rr domain.RerunEnvelope
	require.NoError(t, json.Unmarshal([]byte(rerunJSON), &rr))
	require.Equal(t, "github:octocat", rr.RequestedBy)
	require.NoError(t, rr.Validate())

	// Absent kind ⇒ decision (v1 relay compatibility).
	var legacy domain.DecisionEnvelope
	require.NoError(t, json.Unmarshal([]byte(`{"decision_id":"dec_01J"}`), &legacy))
	require.Empty(t, legacy.Kind, "pre-widening relays carry no kind field")
	kind, err := domain.KindFor("")
	require.NoError(t, err)
	require.Equal(t, domain.KindDecision, kind)
}
