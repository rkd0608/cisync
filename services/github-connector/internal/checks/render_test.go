package checks

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"sauron.dev/sauron/github-connector/internal/domain"
)

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

func TestRenderProducesCompletedCheckPayload(t *testing.T) {
	env := &domain.DecisionEnvelope{
		DecisionID:  "dec_01JTEST",
		CandidateID: "cand_01JTEST",
		Repo:        "acme/payments",
		HeadSHA:     "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Verb:        domain.VerbEligibleForMergeTrain,
		Confidence:  0.94,
		Policy:      domain.PolicyRef{PolicyID: "pol_sauron_default", Version: 1},
		Summary:     "All required evidence accepted",
	}
	payload, err := Render(env, "http://localhost:3000/candidates/cand_01JTEST")
	require.NoError(t, err)

	require.Equal(t, CheckName, payload.Name)
	require.Equal(t, env.HeadSHA, payload.HeadSHA)
	require.Equal(t, "completed", payload.Status)
	require.Equal(t, "success", payload.Conclusion)
	require.Equal(t, env.DecisionID, payload.ExternalID)
	require.Contains(t, payload.Summary, "confidence=0.94")

	raw, err := json.Marshal(payload)
	require.NoError(t, err)
	var decoded map[string]any
	require.NoError(t, json.Unmarshal(raw, &decoded))
	for _, key := range []string{"name", "head_sha", "status", "conclusion"} {
		require.Contains(t, decoded, key)
	}
}
