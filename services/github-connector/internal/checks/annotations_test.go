package checks

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"cisync.dev/cisync/github-connector/internal/domain"
)

func finding(path string, line int, message, kind string) domain.FindingAnnotation {
	return domain.FindingAnnotation{Path: path, StartLine: line, Message: message, Kind: kind}
}

func rejectedEnvelope(findings []domain.FindingAnnotation) *domain.DecisionEnvelope {
	return &domain.DecisionEnvelope{
		DecisionID: "dec_01J", CandidateID: "cand_01J", Repo: "acme/payments",
		HeadSHA: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Verb:    domain.VerbRejected, Confidence: 0.91,
		Policy:      domain.PolicyRef{PolicyID: "pol_cisync_default", Version: 1},
		RenderedAt:  goldenRenderedAt,
		Evidence:    &domain.EvidenceCounts{Required: 3, Accepted: 1, Deferred: 0, Failed: 2},
		Annotations: findings,
	}
}

func TestFailureAnnotationsCarriedOnFailureOnly(t *testing.T) {
	findings := []domain.FindingAnnotation{
		finding("pkg/a.go", 10, "api compat broke", "api_compat"),
		finding("", 0, "sast diff regression", "sast_diff"),
	}
	payload, err := RenderDecision(rejectedEnvelope(findings), detailsBase)
	require.NoError(t, err)
	require.Equal(t, []Annotation{
		{Path: "pkg/a.go", StartLine: 10, EndLine: 10, Message: "api compat broke", Title: "api_compat"},
		{Message: "sast diff regression", Title: "sast_diff"},
	}, payload.Annotations)

	okEnv := rejectedEnvelope(findings)
	okEnv.Verb = domain.VerbEligibleForMergeTrain
	payload, err = RenderDecision(okEnv, detailsBase)
	require.NoError(t, err)
	require.Nil(t, payload.Annotations, "annotations attach ONLY to failure conclusions")

	deferredEnv := rejectedEnvelope(findings)
	deferredEnv.Verb = domain.VerbDeferred
	payload, err = RenderDecision(deferredEnv, detailsBase)
	require.NoError(t, err)
	require.Nil(t, payload.Annotations)
}

func TestAnnotationOverflowCollapsesToFifty(t *testing.T) {
	findings := make([]domain.FindingAnnotation, 0, MaxAnnotationsPerBatch+2)
	for i := range MaxAnnotationsPerBatch + 2 {
		findings = append(findings, finding("pkg/x.go", i+1, "finding", "kind"))
	}
	payload, err := RenderDecision(rejectedEnvelope(findings), detailsBase)
	require.NoError(t, err)
	require.Len(t, payload.Annotations, MaxAnnotationsPerBatch, "GitHub hard cap")
	last := payload.Annotations[len(payload.Annotations)-1]
	require.Equal(t, overflowMessage(3), last.Message, "52 findings ⇒ 49 shown + 3 more")
	require.Equal(t, "dossier", last.Title)

	exactFifty := findings[:MaxAnnotationsPerBatch]
	payload, err = RenderDecision(rejectedEnvelope(exactFifty), detailsBase)
	require.NoError(t, err)
	require.Len(t, payload.Annotations, MaxAnnotationsPerBatch)
	require.Equal(t, "finding", payload.Annotations[49].Message, "no overflow note at exactly 50")
}

// TestGoldenPayloadJSON pins the exact wire bytes of the flagship failure
// payload so dry-run logs stay diffable across releases.
func TestGoldenPayloadJSON(t *testing.T) {
	env := rejectedEnvelope([]domain.FindingAnnotation{
		finding("pkg/a.go", 10, "api compat broke", "api_compat"),
	})
	env.DecisionID = "dec_01JTESTDECISION"
	env.CandidateID = "cand_01JTESTCANDIDATE"
	payload, err := RenderDecision(env, detailsBase)
	require.NoError(t, err)
	raw, err := json.Marshal(payload)
	require.NoError(t, err)
	want := `{"name":"Agent Verification Gate",` +
		`"head_sha":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",` +
		`"status":"completed","conclusion":"failure",` +
		`"details_url":"http://localhost:3000/candidates/cand_01JTESTCANDIDATE",` +
		`"external_id":"cand_01JTESTCANDIDATE",` +
		`"summary":` + strconvQuoteJSON(payload.Summary) + `,` +
		`"annotations":[{"path":"pkg/a.go","start_line":10,"end_line":10,` +
		`"message":"api compat broke","title":"api_compat"}],` +
		`"completed_at":"2026-08-23T03:41:00Z"}`
	require.JSONEq(t, want, string(raw))
}

func strconvQuoteJSON(s string) string {
	raw, _ := json.Marshal(s)
	return string(raw)
}

func TestDetailsURLTrimsTrailingSlashAndHandlesEmptyBase(t *testing.T) {
	payload, err := RenderDecision(rejectedEnvelope(nil), "http://web:3000/")
	require.NoError(t, err)
	require.Equal(t, "http://web:3000/candidates/cand_01J", payload.DetailsURL)
	payload, err = RenderDecision(rejectedEnvelope(nil), "")
	require.NoError(t, err)
	require.Equal(t, "/candidates/cand_01J", payload.DetailsURL)
}
