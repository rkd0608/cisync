package evidence

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"cisync.dev/cisync/control-plane/internal/domain"
)

// The adapter must reject fence mismatches first (I-11 precedence) and pass
// well-formed records through to the provenance validator.
func TestEngineEvaluatorPort(t *testing.T) {
	rec := domain.NewEvidenceRecord("ev_01JTEST", "org_01J", "run_01J", 1, "cand_01J",
		"hermetic_build", VerdictPass, []string{"sha256:" + repeat('a', 64)},
		"sha256:"+repeat('b', 64), 0.9, 10, "fleet:run_01J:3", time.Now().UTC())

	const planInputsHash = "sha256:" + planHashHex

	// The expectations resolver binds proposals to the PLAN's reuse key,
	// never to the record's self-reported hash.
	resolve := func(_ *testing.T) Expectations {
		return func(_ context.Context, _ *domain.EvidenceRecord) (Context, error) {
			return Context{
				ExpectedLeaseJTI:   "fleet:run_01J:3",
				ExpectedInputsHash: planInputsHash,
			}, nil
		}
	}

	t.Run("fence mismatch rejects before provenance", func(t *testing.T) {
		ev := &engineAdapterForTest{resolve: resolve(t)}
		out, err := ev.Evaluate(context.Background(), domain.ProposedEvidence{
			Record: rec, FenceToken: 2, CurrentFence: 3,
		})
		require.NoError(t, err)
		require.False(t, out.Accepted)
		require.Equal(t, "fence_mismatch", out.Reason)
	})

	t.Run("matching lease and inputs hash accepts", func(t *testing.T) {
		ev := &engineAdapterForTest{resolve: resolve(t)}
		out, err := ev.Evaluate(context.Background(), domain.ProposedEvidence{
			Record: rec, FenceToken: 3, CurrentFence: 3,
		})
		require.NoError(t, err)
		require.True(t, out.Accepted)
	})

	t.Run("inputs hash mismatch rejects (I-02)", func(t *testing.T) {
		mismatched := domain.NewEvidenceRecord(rec.ID, "org_01J", "run_01J", 1, "cand_01J",
			"hermetic_build", VerdictPass, nil,
			"sha256:"+repeat('c', 64), 0.9, 10, "fleet:run_01J:3", time.Now().UTC())
		ev := &engineAdapterForTest{resolve: resolve(t)}
		out, err := ev.Evaluate(context.Background(), domain.ProposedEvidence{
			Record: mismatched, FenceToken: 3, CurrentFence: 3,
		})
		require.NoError(t, err)
		require.False(t, out.Accepted)
		require.Equal(t, ReasonInputsHashMismatch, out.Reason)
	})
}

const planHashHex = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"

// engineAdapterForTest binds the Expectations resolver for tests.
type engineAdapterForTest struct {
	resolve Expectations
}

func (e *engineAdapterForTest) Evaluate(ctx context.Context, ev domain.ProposedEvidence) (domain.EvidenceOutcome, error) {
	return NewEngineEvaluator(e.resolve).Evaluate(ctx, ev)
}

func repeat(c byte, n int) string {
	out := make([]byte, n)
	for i := range out {
		out[i] = c
	}
	return string(out)
}
