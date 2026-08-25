package checks

import (
	"context"
	"log/slog"
	"testing"

	"github.com/google/go-github/v66/github"
	"github.com/stretchr/testify/require"

	"sauron.dev/sauron/github-connector/internal/testsupport"
)

func newTestClient(t *testing.T, fake *testsupport.FakeGitHub) *github.Client {
	t.Helper()
	client := github.NewClient(nil)
	base, err := parseURL(fake.BaseURL + "/")
	require.NoError(t, err)
	client.BaseURL = base
	return client
}

func TestLivePublisherCreateOmitsConclusionUntilCompleted(t *testing.T) {
	fake := testsupport.NewFakeGitHub(t)
	pub := NewLivePublisher(newTestClient(t, fake), slog.Default())
	ctx := context.Background()

	queued := CheckPayload{
		Name: CheckName, HeadSHA: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Status: "queued", DetailsURL: "http://web/candidates/cand_01J",
		ExternalID: "cand_01J", Summary: "queued body",
	}
	id, err := pub.Create(ctx, "acme/payments", queued)
	require.NoError(t, err)
	require.Positive(t, id)
	calls := fake.Calls()
	require.Len(t, calls, 1)
	require.Equal(t, "create", calls[0].Method)
	require.Equal(t, "acme", calls[0].Owner)
	require.Equal(t, "payments", calls[0].Repo)
	require.Empty(t, calls[0].Conclusion, "queued creates must NOT carry a conclusion")
	require.Equal(t, "cand_01J", calls[0].ExternalID)

	err = pub.Update(ctx, "acme/payments", id, func() CheckPayload {
		completedAt := goldenRenderedAt
		return CheckPayload{
			Name: CheckName, HeadSHA: queued.HeadSHA, Status: "completed",
			Conclusion: "success", ExternalID: "cand_01J", Summary: "done",
			CompletedAt: &completedAt,
		}
	}())
	require.NoError(t, err)
	calls = fake.Calls()
	require.Len(t, calls, 2)
	require.Equal(t, "update", calls[1].Method)
	require.Equal(t, id, calls[1].CheckRunID)
	require.Equal(t, "success", calls[1].Conclusion)
}

func TestLivePublisherAnnotationsOnWire(t *testing.T) {
	fake := testsupport.NewFakeGitHub(t)
	pub := NewLivePublisher(newTestClient(t, fake), slog.Default())
	payload := CheckPayload{
		Name: CheckName, HeadSHA: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Status: "completed", Conclusion: "failure", ExternalID: "cand_01J",
		Summary: "failed", Annotations: []Annotation{
			{Path: "pkg/a.go", StartLine: 10, EndLine: 10, Message: "broke", Title: "api_compat"},
			{Message: "file-level", Title: "kind"},
		},
	}
	_, err := pub.Create(context.Background(), "acme/payments", payload)
	require.NoError(t, err)
	calls := fake.Calls()
	require.Len(t, calls, 1)
	require.Len(t, calls[0].Annotations, 2)
	require.Equal(t, "pkg/a.go", calls[0].Annotations[0].Path)
	require.Equal(t, 10, calls[0].Annotations[0].StartLine)
	require.Equal(t, 10, calls[0].Annotations[0].EndLine, "end_line must equal start_line")
	require.Empty(t, calls[0].Annotations[1].Path, "pathless finding stays pathless")
}

func TestDryRunPublisherEmitsDeterministicLines(t *testing.T) {
	var sink sinkBuffer
	pub := NewDryRunPublisher(&sink)
	completedAt := goldenRenderedAt
	payload := CheckPayload{
		Name: CheckName, HeadSHA: "aaaa", Status: "completed", Conclusion: "neutral",
		ExternalID: "cand_01J", Summary: "s", CompletedAt: &completedAt,
	}
	_, err := pub.Create(context.Background(), "acme/payments", payload)
	require.NoError(t, err)
	require.NoError(t, pub.Update(context.Background(), "acme/payments", 7, payload))
	require.Equal(t,
		`DRYRUN create repo=acme/payments payload={"name":"Agent Verification Gate","head_sha":"aaaa","status":"completed","conclusion":"neutral","details_url":"","external_id":"cand_01J","summary":"s","completed_at":"2026-08-23T03:41:00Z"}`+"\n"+
			"DRYRUN update repo=acme/payments check_run_id=7 payload="+payloadJSONOf(payload)+"\n",
		sink.String())
}

func payloadJSONOf(p CheckPayload) string {
	raw, _ := marshalPayload(p)
	return string(raw)
}

type sinkBuffer struct{ data []byte }

func (s *sinkBuffer) Write(p []byte) (int, error) {
	s.data = append(s.data, p...)
	return len(p), nil
}

func (s *sinkBuffer) String() string { return string(s.data) }
