package api

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"sauron.dev/sauron/github-connector/internal/checks"
	"sauron.dev/sauron/github-connector/internal/domain"
	"sauron.dev/sauron/github-connector/internal/emit"
	"sauron.dev/sauron/github-connector/internal/ghauth"
	"sauron.dev/sauron/github-connector/internal/obs"
	"sauron.dev/sauron/github-connector/internal/queue"
	"sauron.dev/sauron/github-connector/internal/ratelimit"
	"sauron.dev/sauron/github-connector/internal/rerun"
	"sauron.dev/sauron/github-connector/internal/testsupport"
	"sauron.dev/sauron/github-connector/internal/tracking"
)

// TestLiveLoopThroughFakeGitHub drives envelope → emit.Router →
// ghauth.Registry → fake GitHub: asserts the queued→in_progress→completed
// call sequence, per-repo token scoping claims, and budget-exhaustion queuing
// (plan §7.5 posture, connector-local slice).
func TestLiveLoopThroughFakeGitHub(t *testing.T) {
	fake := testsupport.NewFakeGitHub(t)

	var resolver stubResolverLive
	budget := ratelimit.NewBudget(300, nil)
	gate := ratelimit.NewGate(budget, quietLogger())
	gate.SetSleeper(func(time.Duration) {})
	dry := checks.NewDryRunPublisher(&discardSink{})
	router := emit.NewRouter(dry, &resolver,
		ghauth.NewRegistry("app_1", "", ghauth.WithBaseURL(fake.BaseURL), ghauth.WithKey(testKeyRSA())),
		gate, budget, queue.NewMemoryStore(nil), obs.New(), quietLogger())

	tracker := tracking.NewMemoryStore(nil)
	handler := NewDecisionsHandler(testSecret, HandlerDeps{
		Tracker: tracker, Router: router, Metrics: obs.New(),
		DetailsURL: "http://localhost:3000", RerunPolicy: rerun.PolicyReplan,
		RerunBudget:  rerun.NewBudget(2, 20, nil),
		RerunControl: rerun.NewControl("", "", nil, nil),
		RerunSeen:    rerun.NewDedupe(time.Hour, nil),
	}, quietLogger())
	h := &harness{router: &recordingRouter{}, handler: handler}
	_ = h

	post := func(body any, key string) *http.Response {
		raw, err := json.Marshal(body)
		require.NoError(t, err)
		req, _ := http.NewRequest(http.MethodPost, "/", bytesReader(raw))
		req.Header.Set("X-Sauron-Signature", signBody([]byte(testSecret), raw))
		req.Header.Set("Idempotency-Key", key)
		rec := recordResponse()
		handler.ServeHTTP(rec, req)
		return rec.Result()
	}

	cand := "cand_01JLOOP"
	sha := "dddddddddddddddddddddddddddddddddddddddd"

	require.Equal(t, http.StatusAccepted, post(lifecycleEnvelopeFor(cand, sha), cand+":queued").StatusCode)
	require.Equal(t, http.StatusAccepted, post(lifecycleEnvelopeFor(cand, sha, domain.LifecycleInProgress), cand+":in_progress").StatusCode)
	require.Equal(t, http.StatusAccepted, post(decisionEnvelopeFor(cand, sha, "dec_01JLOOP"), "dec_01JLOOP").StatusCode)

	calls := fake.Calls()
	require.Len(t, calls, 3, "create + update + update — ONE run walked in place")
	require.Equal(t, []string{"queued", "in_progress", "completed"}, statusesOf(calls))
	require.Equal(t, cand, calls[0].ExternalID, "external_id = candidate_id")

	mints := fake.Tokens()
	require.NotEmpty(t, mints)
	for _, mint := range mints {
		require.JSONEq(t, `{"repositories":["payments"],"permissions":{"checks":"write"}}`,
			mint.Body, "every mint is repo-scoped")
	}
}

// TestBudgetExhaustionQueuesThenDrains proves the never-drop guarantee:
// a drained budget defers the write into the pending queue, and the drainer
// flushes it to GitHub once tokens return (plan §4.6).
func TestBudgetExhaustionQueuesThenDrains(t *testing.T) {
	fake := testsupport.NewFakeGitHub(t)
	now := frozenNow
	clock := &now

	var resolver stubResolverLive
	budget := ratelimit.NewBudget(1, func() time.Time { return *clock })
	gate := ratelimit.NewGate(budget, quietLogger())
	gate.SetSleeper(func(time.Duration) {})
	pending := queue.NewMemoryStore(func() time.Time { return *clock })
	dry := checks.NewDryRunPublisher(discardSink{})
	router := emit.NewRouter(dry, &resolver,
		ghauth.NewRegistry("app_1", "", ghauth.WithBaseURL(fake.BaseURL), ghauth.WithKey(testKeyRSA())),
		gate, budget, pending, obs.New(), quietLogger())

	tracker := tracking.NewMemoryStore(nil)
	handler := NewDecisionsHandler(testSecret, HandlerDeps{
		Tracker: tracker, Router: router, Metrics: obs.New(),
		DetailsURL: "http://localhost:3000", RerunPolicy: rerun.PolicyReplan,
		RerunBudget:  rerun.NewBudget(2, 20, nil),
		RerunControl: rerun.NewControl("", "", nil, nil),
		RerunSeen:    rerun.NewDedupe(time.Hour, nil),
	}, quietLogger())

	post := func(body any, key string) int {
		raw, err := json.Marshal(body)
		require.NoError(t, err)
		req, _ := http.NewRequest(http.MethodPost, "/", bytesReader(raw))
		req.Header.Set("X-Sauron-Signature", signBody([]byte(testSecret), raw))
		req.Header.Set("Idempotency-Key", key)
		rec := recordResponse()
		handler.ServeHTTP(rec, req)
		return rec.Code
	}

	cand := "cand_01JBUDGET"
	sha := "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"
	require.Equal(t, http.StatusAccepted, post(lifecycleEnvelopeFor(cand, sha), cand+":queued"))

	// Second write hits the empty 1/hour budget ⇒ queued, still accepted.
	*clock = now.Add(time.Second)
	require.Equal(t, http.StatusAccepted, post(decisionEnvelopeFor(cand, sha, "dec_01JBUDGET"), "dec_01JBUDGET"))
	require.Equal(t, 1, pending.Depth())

	// An hour later the drainer flushes the deferred completed write.
	*clock = now.Add(time.Hour)
	drainer := queue.NewDrainer(pending, gate, budget,
		func(ctx context.Context, w queue.PendingWrite) error {
			_, err := router.PublishDirect(ctx, w.Repo, w.CheckRunID, w.Payload)
			return err
		},
		time.Minute, func() time.Time { return *clock }, quietLogger(), nil)
	drainer.Tick(context.Background())

	calls := fake.Calls()
	require.Len(t, calls, 2, "queued write reached GitHub after refill")
	require.Equal(t, []string{"queued", "completed"}, statusesOf(calls))
	require.Equal(t, 0, pending.Depth())
}

func lifecycleEnvelopeFor(cand, sha string, phase ...domain.LifecyclePhase) domain.LifecycleEnvelope {
	p := domain.LifecycleQueued
	if len(phase) > 0 {
		p = phase[0]
	}
	return domain.LifecycleEnvelope{Kind: domain.KindLifecycle, Phase: p,
		CandidateID: cand, Repo: "acme/payments", HeadSHA: sha, At: frozenNow}
}

func decisionEnvelopeFor(cand, sha, decisionID string) domain.DecisionEnvelope {
	return domain.DecisionEnvelope{Kind: domain.KindDecision,
		DecisionID: decisionID, CandidateID: cand, Repo: "acme/payments",
		HeadSHA: sha, Verb: domain.VerbEligibleForMergeTrain, Confidence: 0.9,
		Policy:     domain.PolicyRef{PolicyID: "pol_sauron_default", Version: 1},
		RenderedAt: frozenNow,
		Evidence:   &domain.EvidenceCounts{Required: 2, Accepted: 2},
	}
}

func statusesOf(calls []testsupport.CheckCall) []string {
	out := make([]string, 0, len(calls))
	for _, c := range calls {
		out = append(out, c.Status)
	}
	return out
}

// stubResolverLive resolves everything to installation 7.
type stubResolverLive struct{}

func (s *stubResolverLive) ResolveInstallation(context.Context, string, string) (int64, error) {
	return 7, nil
}
