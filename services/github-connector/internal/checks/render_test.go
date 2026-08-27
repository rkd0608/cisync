package checks

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"cisync.dev/cisync/github-connector/internal/domain"
)

var (
	goldenRenderedAt = time.Date(2026, 8, 23, 3, 41, 0, 0, time.UTC)
	goldenSweptAt    = time.Date(2026, 8, 24, 0, 0, 0, 0, time.UTC)
	detailsBase      = "http://localhost:3000"
)

func goldenEvidence() *domain.EvidenceCounts {
	return &domain.EvidenceCounts{Required: 5, Accepted: 5, Deferred: 2, Failed: 0}
}

func TestConclusionForVerbMapping(t *testing.T) {
	cases := map[domain.DecisionVerb]string{
		domain.VerbEligibleForMergeTrain: "success",
		domain.VerbRejected:              "failure",
		domain.VerbDeferred:              "neutral",
	}
	for verb, want := range cases {
		got, err := ConclusionForVerb(verb)
		require.NoError(t, err, "verb %s", verb)
		require.Equal(t, want, got)
	}
	_, err := ConclusionForVerb("combine")
	require.Error(t, err, "unsupported verbs must fail closed")
}

// TestGoldenDecisionSummaries freezes the THIN §4.3 summary bytes per verb
// (W6): verb+confidence+policy header, evidence counts and details_url ONLY.
// The dossier intelligence lives in the sticky PR comment from now on.
func TestGoldenDecisionSummaries(t *testing.T) {
	cases := map[domain.DecisionVerb]string{
		domain.VerbEligibleForMergeTrain: "**Eligible for merge train**",
		domain.VerbRejected:              "**Rejected**",
		domain.VerbDeferred:              "**Deferred**",
	}
	for verb, headline := range cases {
		env := &domain.DecisionEnvelope{
			Kind: domain.KindDecision, DecisionID: "dec_01JTESTDECISION",
			CandidateID: "cand_01JTESTCANDIDATE", Repo: "acme/payments",
			HeadSHA: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			Verb:    verb, Confidence: 0.94,
			Policy:     domain.PolicyRef{PolicyID: "pol_cisync_default", Version: 1},
			Summary:    "explanation summary",
			RenderedAt: goldenRenderedAt, Evidence: goldenEvidence(),
		}
		payload, err := RenderDecision(env, detailsBase)
		require.NoError(t, err, verb)
		want := headline + " · confidence 0.94 · policy pol_cisync_default v1\n" +
			"Evidence: 5/5 required accepted · 2 deferred (reason-linked) · 0 failed\n" +
			"→ Full dossier: http://localhost:3000/candidates/cand_01JTESTCANDIDATE"
		require.Equal(t, want, payload.Summary, "byte-frozen THIN summary for %s", verb)
		require.Equal(t, "completed", payload.Status)
		require.Equal(t, env.CandidateID, payload.ExternalID, "B1/G6: external_id is candidate_id")
		require.Empty(t, payload.Annotations, "annotations only on failure")
	}
}

// TestGoldenLegacySummary pins the pre-widening flat format for relays that
// do not yet send evidence counts.
func TestGoldenLegacySummary(t *testing.T) {
	env := &domain.DecisionEnvelope{
		DecisionID: "dec_01J", CandidateID: "cand_01J", Repo: "acme/payments",
		HeadSHA: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Verb:    domain.VerbEligibleForMergeTrain, Confidence: 0.94,
		Policy:  domain.PolicyRef{PolicyID: "pol_x", Version: 4},
		Summary: "All required evidence accepted", RenderedAt: goldenRenderedAt,
	}
	payload, err := RenderDecision(env, detailsBase)
	require.NoError(t, err)
	require.Equal(t,
		"All required evidence accepted (verb=eligible_for_merge_train confidence=0.94 policy=pol_x v4)",
		payload.Summary)
}

func TestGoldenCachedSummary(t *testing.T) {
	env := &domain.DecisionEnvelope{
		DecisionID: "dec_01J", CandidateID: "cand_01J", Repo: "acme/payments",
		HeadSHA: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Verb:    domain.VerbDeferred, Confidence: 0.5,
		Policy:     domain.PolicyRef{PolicyID: "pol_cisync_default", Version: 1},
		RenderedAt: goldenRenderedAt, Evidence: goldenEvidence(),
	}
	payload, err := RenderCached(env, detailsBase)
	require.NoError(t, err)
	want := "**Deferred** · confidence 0.50 · policy pol_cisync_default v1\n" +
		"Evidence: 5/5 required accepted · 2 deferred (reason-linked) · 0 failed\n" +
		"→ Full dossier: http://localhost:3000/candidates/cand_01J\n" +
		"_cached replay (no recompute)_"
	require.Equal(t, want, payload.Summary)
	require.Equal(t, "neutral", payload.Conclusion)
}

func TestGoldenLifecycleSummaries(t *testing.T) {
	queued := &domain.LifecycleEnvelope{
		Kind: domain.KindLifecycle, Phase: domain.LifecycleQueued,
		CandidateID: "cand_01J", Repo: "acme/payments",
		HeadSHA: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", At: goldenRenderedAt,
	}
	payload, err := RenderLifecycle(queued, detailsBase)
	require.NoError(t, err)
	want := "**Queued** · CISync accepted candidate cand_01J for verification\n" +
		"→ Full dossier: http://localhost:3000/candidates/cand_01J\n" +
		"_queued 2026-08-23T03:41:00Z_"
	require.Equal(t, want, payload.Summary)
	require.Equal(t, "queued", payload.Status)
	require.Empty(t, payload.Conclusion, "non-terminal phases never carry conclusions")
	require.Nil(t, payload.CompletedAt)

	inProgress := *queued
	inProgress.Phase = domain.LifecycleInProgress
	payload, err = RenderLifecycle(&inProgress, detailsBase)
	require.NoError(t, err)
	want = "**In progress** · CISync verification started for cand_01J\n" +
		"→ Full dossier: http://localhost:3000/candidates/cand_01J\n" +
		"_in progress since 2026-08-23T03:41:00Z_"
	require.Equal(t, want, payload.Summary)
	require.Equal(t, "in_progress", payload.Status)
}

func TestGoldenStalledAndDeclinedSummaries(t *testing.T) {
	stalled := RenderStalled("cand_01J", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", detailsBase, goldenSweptAt)
	want := "**Stalled** · no verification result within the time budget\n" +
		"→ Full dossier: http://localhost:3000/candidates/cand_01J\n" +
		"_flipped neutral by stalled-check sweeper 2026-08-24T00:00:00Z_"
	require.Equal(t, want, stalled.Summary)
	require.Equal(t, "neutral", stalled.Conclusion)

	exhausted := RenderRerunDeclined("cand_01J", "aaaa", detailsBase, goldenSweptAt, true)
	require.Equal(t, "**Re-run budget exhausted** · cap reached for this candidate or hour\n"+
		"→ Full dossier: http://localhost:3000/candidates/cand_01J\n"+
		"_re-run declined 2026-08-24T00:00:00Z · see dossier for the standing verdict_",
		exhausted.Summary)

	unavailable := RenderRerunDeclined("cand_01J", "aaaa", detailsBase, goldenSweptAt, false)
	require.Equal(t, "**Re-run unavailable** · control-plane unreachable; request not lost\n"+
		"→ Full dossier: http://localhost:3000/candidates/cand_01J\n"+
		"_re-run declined 2026-08-24T00:00:00Z · retry the GitHub re-run later_",
		unavailable.Summary)
}
