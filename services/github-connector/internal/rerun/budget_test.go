package rerun

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestParsePolicyDefaultsToReplan(t *testing.T) {
	for raw, want := range map[string]Policy{
		"":                PolicyReplan,
		"replan":          PolicyReplan,
		"replay_cached":   PolicyReplayCached,
		" REPLAY_CACHED ": PolicyReplayCached,
	} {
		got, err := ParsePolicy(raw)
		require.NoError(t, err, raw)
		require.Equal(t, want, got)
	}
	_, err := ParsePolicy("yolo")
	require.Error(t, err)
}

func TestBudgetCapsPerCandidateAndPerHour(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	clock := now
	budget := NewBudget(2, 3, func() time.Time { return clock })

	// Per-candidate cap of 2.
	require.True(t, budget.Allow("cand_A", 1).Allowed)
	budget.Record("cand_A", 1)
	require.True(t, budget.Allow("cand_A", 1).Allowed)
	budget.Record("cand_A", 1)
	verdict := budget.Allow("cand_A", 1)
	require.False(t, verdict.Allowed)
	require.Equal(t, "candidate_cap", verdict.Reason)
	require.True(t, budget.Allow("cand_B", 1).Allowed, "other candidates unaffected")

	// Hourly cap of 3 per installation: cand_B used one slot.
	budget.Record("cand_C", 1)
	budget.Record("cand_D", 2)
	verdict = budget.Allow("cand_E", 1)
	require.False(t, verdict.Allowed)
	require.Equal(t, "hour_rate", verdict.Reason)
	require.True(t, budget.Allow("cand_F", 9).Allowed, "other installations unaffected")

	// WHY fixed window: the hour bucket rolls over and caps reset.
	clock = now.Add(time.Hour + time.Minute)
	require.True(t, budget.Allow("cand_E", 1).Allowed)
}

func TestDedupeFirstSeenAndTTLExpiry(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	clock := now
	dedupe := NewDedupe(time.Hour, func() time.Time { return clock })

	require.True(t, dedupe.FirstSeen("delivery-1"))
	require.False(t, dedupe.FirstSeen("delivery-1"), "replays collapse")
	clock = clock.Add(2 * time.Hour)
	require.True(t, dedupe.FirstSeen("delivery-1"), "TTL expiry allows reprocessing")
}

func TestControlRevalidateContract(t *testing.T) {
	var hits int32
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		gotAuth = r.Header.Get("Authorization")
		if r.URL.Path == "/v1/candidates/cand_ok/revalidate" && r.Method == http.MethodPost {
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(`{"accepted":true}`))
			return
		}
		if r.URL.Path == "/v1/candidates/cand_404/revalidate" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	control := NewControl(srv.URL, "admin_token", srv.Client(), nil)
	require.NoError(t, control.Revalidate(context.Background(), "cand_ok", "00000000-0000-4821-9f10-00000000dead"))
	require.Equal(t, int32(1), atomic.LoadInt32(&hits))
	require.Equal(t, "Bearer admin_token", gotAuth)

	err := control.Revalidate(context.Background(), "cand_404", "00000000-0000-4821-9f10-00000000dead")
	var unknown *ErrUnknownCandidate
	require.True(t, errors.As(err, &unknown), "404 maps to typed unknown candidate")

	require.Error(t, control.Revalidate(context.Background(), "cand_authfail", "00000000-0000-4821-9f10-00000000dead"))
}

func TestControlDisabledWithoutBaseURL(t *testing.T) {
	control := NewControl("", "", nil, nil)
	require.False(t, control.Enabled())
	require.Error(t, control.Revalidate(context.Background(), "cand_x", "00000000-0000-4821-9f10-00000000dead"))
}
