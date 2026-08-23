package evidence

import (
	"fmt"
	"sort"
	"testing"

	"github.com/stretchr/testify/require"
	"pgregory.net/rapid"
)

var (
	validHash  = "sha256:" + hexOf("ab")
	validHash2 = "sha256:" + hexOf("cd")
	validDgst  = "sha256:" + hexOf("ef")
)

func hexOf(pair string) string {
	out := ""
	for i := 0; i < 32; i++ {
		out += pair
	}
	return out
}

func validRecord() ProposedRecord {
	return ProposedRecord{
		ID: "ev_1", RunID: "run_1", CandidateID: "cand_1", Kind: "selected_unit",
		Verdict: VerdictPass, Outcome: OutcomePassed,
		Digests: []string{validDgst}, InputsHash: validHash,
		LeaseJTI: "lease_1", Attempt: 1,
	}
}

func validContext() Context {
	return Context{
		ExpectedLeaseJTI:   "lease_1",
		ExpectedInputsHash: validHash,
	}
}

func TestEvaluateAcceptsValidRecord(t *testing.T) {
	got := Evaluate(validRecord(), validContext())
	require.Equal(t, ActionAccept, got.Action)
	require.Empty(t, got.Reason)
}

func TestEvaluateRejectionBranches(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*ProposedRecord, *Context)
		want   Action
		reason string
	}{
		{"missing id", func(p *ProposedRecord, _ *Context) { p.ID = "" }, ActionReject, ReasonMalformed},
		{"bad digest format", func(p *ProposedRecord, _ *Context) { p.Digests = []string{"deadbeef"} }, ActionReject, ReasonMalformed},
		{"attempt zero", func(p *ProposedRecord, _ *Context) { p.Attempt = 0 }, ActionReject, ReasonMalformed},
		{"wrong lease", func(_ *ProposedRecord, c *Context) { c.ExpectedLeaseJTI = "lease_2" }, ActionReject, ReasonProvenanceMismatch},
		{"empty expected lease", func(_ *ProposedRecord, c *Context) { c.ExpectedLeaseJTI = "" }, ActionReject, ReasonProvenanceMismatch},
		{"inputs hash mismatch (I-02)", func(p *ProposedRecord, _ *Context) { p.InputsHash = validHash2 }, ActionReject, ReasonInputsHashMismatch},
		{"empty expected inputs hash (fail-closed)", func(p *ProposedRecord, c *Context) {
			p.InputsHash = ""
			c.ExpectedInputsHash = ""
		}, ActionReject, ReasonInputsHashMismatch},
		{"duplicate run attempt (I-03)", func(_ *ProposedRecord, c *Context) {
			c.Accepted = append(c.Accepted, AcceptedRef{RunID: "run_1", Attempt: 1})
		}, ActionReject, ReasonDuplicateAttempt},
		{"lease jti reuse (I-03)", func(_ *ProposedRecord, c *Context) {
			c.Accepted = append(c.Accepted, AcceptedRef{RunID: "run_other", Attempt: 9, LeaseJTI: "lease_1"})
		}, ActionReject, ReasonLeaseAlreadyAccepted},
		{"skipped outcome (I-01)", func(p *ProposedRecord, _ *Context) { p.Outcome = OutcomeSkipped }, ActionReject, ReasonSkipNeverPositive},
		{"filtered outcome (I-01)", func(p *ProposedRecord, _ *Context) { p.Outcome = OutcomeFiltered }, ActionReject, ReasonSkipNeverPositive},
		{"quarantined outcome (I-01)", func(p *ProposedRecord, _ *Context) { p.Outcome = OutcomeQuarantined }, ActionReject, ReasonSkipNeverPositive},
		{"pass verdict on failed outcome", func(p *ProposedRecord, _ *Context) { p.Outcome = OutcomeFailed }, ActionReject, ReasonVerdictUnsupported},
		{"unknown outcome", func(p *ProposedRecord, _ *Context) { p.Outcome = "crashed" }, ActionReject, ReasonVerdictUnsupported},
		{"digest manifest mismatch ⇒ quarantine", func(_ *ProposedRecord, c *Context) {
			c.ExpectedDigests = []string{validHash}
		}, ActionQuarantine, ReasonDigestManifestMismatch},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := validRecord()
			ctx := validContext()
			tc.mutate(&rec, &ctx)
			got := Evaluate(rec, ctx)
			require.Equal(t, tc.want, got.Action)
			require.Equal(t, tc.reason, got.Reason)
		})
	}
}

func TestEvaluateManifestOrderInsensitive(t *testing.T) {
	rec := validRecord()
	rec.Digests = []string{validHash, validDgst}
	ctx := validContext()
	ctx.ExpectedDigests = []string{validDgst, validHash}
	require.Equal(t, ActionAccept, Evaluate(rec, ctx).Action)
}

func TestSufficiencyD8(t *testing.T) {
	passAccepted := func(kinds ...string) []AcceptedRecord {
		var out []AcceptedRecord
		for _, k := range kinds {
			out = append(out, AcceptedRecord{Kind: k, Verdict: VerdictPass, Status: StatusAccepted})
		}
		return out
	}
	require.InDelta(t, 1.0, Sufficiency(nil, nil), 1e-12, "vacuously satisfied")
	require.InDelta(t, 0.5, Sufficiency([]string{"a", "b"}, passAccepted("a")), 1e-12)
	require.InDelta(t, 1.0, Sufficiency([]string{"a", "a", "b"}, passAccepted("a", "b")), 1e-12, "dedupe required kinds")
	require.InDelta(t, 0.0, Sufficiency([]string{"a"}, []AcceptedRecord{
		{Kind: "a", Verdict: VerdictFail, Status: StatusAccepted},
	}), 1e-12, "failed records never count")
	require.InDelta(t, 0.0, Sufficiency([]string{"a"}, []AcceptedRecord{
		{Kind: "a", Verdict: VerdictPass, Status: "proposed"},
	}), 1e-12, "non-accepted status never counts")
}

// --- property tests ---

// TestPropertyInvariantsHoldForAdversarialRecords mutates a fully-valid
// record with a random subset of violations; acceptance must occur if and
// only if NO mutation was applied.
func TestPropertyInvariantsHoldForAdversarialRecords(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		rec := validRecord()
		ctx := validContext()

		type mutation struct {
			name  string
			apply func()
		}
		mutations := []mutation{
			{"skip_like", func() {
				rec.Outcome = rapid.SampledFrom([]string{OutcomeSkipped, OutcomeQuarantined, OutcomeFiltered}).Draw(t, "skip_kind")
			}},
			{"hash_mismatch", func() {
				rec.InputsHash = fmt.Sprintf("sha256:%064d", rapid.IntRange(0, 9).Draw(t, "other_hash"))
			}},
			{"duplicate_attempt", func() {
				ctx.Accepted = append(ctx.Accepted, AcceptedRef{RunID: rec.RunID, Attempt: rec.Attempt})
			}},
			{"jti_reuse", func() {
				ctx.Accepted = append(ctx.Accepted, AcceptedRef{RunID: "run_x", Attempt: 77, LeaseJTI: rec.LeaseJTI})
			}},
			{"lease_swap", func() { ctx.ExpectedLeaseJTI = "lease_other" }},
			{"verdict_flip", func() { rec.Verdict = VerdictFail }},
		}
		applied := false
		for _, m := range mutations {
			if rapid.Bool().Draw(t, "mutate_"+m.name) {
				m.apply()
				applied = true
			}
		}
		got := Evaluate(rec, ctx)
		if !applied {
			require.Equal(t, ActionAccept, got.Action, "unmutated valid record must be accepted")
		} else {
			require.NotEqual(t, ActionAccept, got.Action, "every adversarial mutation blocks acceptance")
		}
	})
}

// TestPropertyI01SkipNeverPasses sweeps arbitrary outcome/verdict pairs:
// skip-like outcomes NEVER yield acceptance (I-01).
func TestPropertyI01SkipNeverPasses(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		rec := ProposedRecord{
			ID: "ev_p", RunID: "run_p", CandidateID: "cand_p", Kind: "k",
			Verdict: rapid.SampledFrom([]string{VerdictPass, VerdictFail, "", "PASS"}).Draw(t, "verdict"),
			Outcome: rapid.SampledFrom([]string{
				OutcomeSkipped, OutcomeQuarantined, OutcomeFiltered, "SKIP", "",
			}).Draw(t, "outcome"),
			InputsHash: validHash, LeaseJTI: "lease_1", Attempt: 1,
		}
		got := Evaluate(rec, validContext())
		require.NotEqual(t, ActionAccept, got.Action,
			"skipped/quarantined/filtered can never be accepted evidence (I-01)")
	})
}

// TestPropertySufficiencyBoundedAndMonotonic: sufficiency stays in [0,1] and
// never decreases as valid accepted records accumulate; full coverage of all
// distinct required kinds yields exactly 1.0.
func TestPropertySufficiencyBoundedAndMonotonic(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		n := rapid.IntRange(0, 8).Draw(t, "n")
		requiredSet := map[string]struct{}{}
		required := make([]string, 0, n)
		for i := 0; i < n; i++ {
			k := fmt.Sprintf("kind_%d", rapid.IntRange(0, 9).Draw(t, "req_kind"))
			if _, dup := requiredSet[k]; !dup {
				requiredSet[k] = struct{}{}
				required = append(required, k)
			}
		}
		sort.Strings(required)

		var accepted []AcceptedRecord
		prev := -1.0
		for _, k := range required {
			accepted = append(accepted, AcceptedRecord{Kind: k, Verdict: VerdictPass, Status: StatusAccepted})
			s := Sufficiency(required, accepted)
			require.GreaterOrEqual(t, s, prev, "sufficiency must be monotonic in accepted set")
			require.LessOrEqual(t, s, 1.0)
			prev = s
		}
		if len(required) > 0 {
			require.InDelta(t, 1.0, prev, 1e-12, "all required kinds covered ⇒ 1.0")
		} else {
			require.InDelta(t, 1.0, Sufficiency(nil, nil), 1e-12)
		}
	})
}
