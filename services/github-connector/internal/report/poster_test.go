package report

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"cisync.dev/cisync/github-connector/internal/domain"
	"cisync.dev/cisync/github-connector/internal/ghauth"
	"cisync.dev/cisync/github-connector/internal/obs"
	"cisync.dev/cisync/github-connector/internal/testsupport"
)

func mustRSAKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	return key
}

func quietReporterLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))
}

type alwaysResolve struct{}

func (alwaysResolve) ResolveInstallation(context.Context, string, string) (int64, error) {
	return 7, nil
}

var errUnresolvedForTest = errors.New("no installation")

type neverResolve struct{}

func (neverResolve) ResolveInstallation(context.Context, string, string) (int64, error) {
	return 0, errUnresolvedForTest
}

// decisionFor builds a fully valid push for one PR; changesByCall disambiguates
// successive posts on the same sticky comment in assertions.
func decisionFor(decisionID string, confidence float64) domain.DecisionEnvelope {
	env := goldenEnv()
	env.DecisionID = decisionID
	env.Confidence = confidence
	env.PRNumber = 17
	return env
}

func newPoster(t *testing.T, fake *testsupport.FakeGitHub,
	resolver InstallationResolver) *Poster {
	t.Helper()
	registry := ghauth.NewRegistry("app_1", "", ghauth.WithBaseURL(fake.BaseURL),
		ghauth.WithKey(mustRSAKey(t)))
	return NewPoster(resolver, registry, goldenDetails, obs.New(), quietReporterLogger())
}

func splitCalls(calls []testsupport.IssueCommentCall) (creates, edits []testsupport.IssueCommentCall) {
	for _, c := range calls {
		switch c.Method {
		case "create":
			creates = append(creates, c)
		case "edit":
			edits = append(edits, c)
		}
	}
	return creates, edits
}

// TestPosterCreateOnceThenUpdateInPlace is the core sticky-comment contract:
// two pushes on the same PR yield exactly ONE create call and ONE edit call —
// two comment-write API hits, not two comments.
func TestPosterCreateOnceThenUpdateInPlace(t *testing.T) {
	fake := testsupport.NewFakeGitHub(t)
	poster := newPoster(t, fake, alwaysResolve{})
	ctx := context.Background()

	first := decisionFor("dec_01JA", 0.94)
	second := decisionFor("dec_01JB", 0.55)
	require.NoError(t, poster.Post(ctx, &first))
	require.NoError(t, poster.Post(ctx, &second))

	calls := fake.IssueCalls()
	creates, edits := splitCalls(calls)
	require.Len(t, creates, 1, "comment created exactly once")
	require.Len(t, edits, 1, "second push PATCHes the same comment in place")
	require.Len(t, fake.IssueComments(), 1, "never a second comment")

	stored := onlyComment(t, fake)
	require.True(t, startsWithMarker(stored.Body), "marker line must be first")
	require.Contains(t, stored.Body, "confidence 0.55 (low)")
	require.NotContains(t, stored.Body, "confidence 0.94",
		"in-place patch replaced stale report bytes")
}

func TestPosterEditsOnlyItsOwnMarkerComment(t *testing.T) {
	fake := testsupport.NewFakeGitHub(t)
	foreignID := fake.SeedForeignIssueComment(17,
		"Mentions <!-- cisync:report --> mid-body but is human chatter")
	poster := newPoster(t, fake, alwaysResolve{})
	ctx := context.Background()

	env := decisionFor("dec_01JC", 0.94)
	require.NoError(t, poster.Post(ctx, &env))
	second := decisionFor("dec_01JD", 0.2)
	require.NoError(t, poster.Post(ctx, &second))

	stored := onlyOursComment(t, fake, foreignID)
	require.True(t, startsWithMarker(stored.Body))
	foreign := fake.IssueComments()[foreignID]
	require.Equal(t,
		"Mentions <!-- cisync:report --> mid-body but is human chatter", foreign.Body,
		"third-party comments are never edited")

	_, edits := splitCalls(fake.IssueCalls())
	for _, e := range edits {
		require.NotEqual(t, foreignID, e.ID, "foreign comment id must be untouched")
	}
	require.Len(t, edits, 1)
}

func TestPosterUnresolvedInstallationFailsClosed(t *testing.T) {
	fake := testsupport.NewFakeGitHub(t)
	poster := newPoster(t, fake, neverResolve{})
	env := decisionFor("dec_01JE", 0.5)
	err := poster.Post(context.Background(), &env)
	require.ErrorIs(t, err, errUnresolvedForTest)
	require.Empty(t, fake.IssueComments(), "no comment without a proven installation")
}

// TestPosterCreateFailureSurfaces drives the whole loop against a hard-500
// GitHub: token minting and/or the comment write must surface as an error
// and land in the failure counter — never a silent drop.
func TestPosterCreateFailureSurfaces(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)
	metrics := obs.New()
	registry := ghauth.NewRegistry("app_1", "", ghauth.WithBaseURL(srv.URL),
		ghauth.WithKey(mustRSAKey(t)))
	poster := NewPoster(alwaysResolve{}, registry, goldenDetails, metrics, quietReporterLogger())

	env := decisionFor("dec_01JZ", 0.9)
	require.Error(t, poster.Post(context.Background(), &env))

	snapshot := metrics.Render()
	require.Contains(t, snapshot, "cisync_report_post_failures_total",
		"failure is visible in metrics, not swallowed")
}

func onlyComment(t *testing.T, fake *testsupport.FakeGitHub) testsupport.StoredIssueComment {
	t.Helper()
	all := fake.IssueComments()
	if len(all) != 1 {
		t.Fatalf("expected one comment, got %d", len(all))
	}
	for _, v := range all {
		return v
	}
	return testsupport.StoredIssueComment{}
}

func onlyOursComment(t *testing.T, fake *testsupport.FakeGitHub, excludeID int64) testsupport.StoredIssueComment {
	t.Helper()
	all := fake.IssueComments()
	if len(all) != 2 {
		t.Fatalf("expected two comments (ours + foreign), got %d", len(all))
	}
	for id, v := range all {
		if id != excludeID {
			return v
		}
	}
	return testsupport.StoredIssueComment{}
}
