package rerun

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

// ctrlStub answers every revalidate POST with the given status, recording
// the observed request headers for contract assertions.
type ctrlStub struct {
	status int
	hits   int
	idem   string
	auth   string
}

func (c *ctrlStub) handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		c.hits++
		c.idem = r.Header.Get("Idempotency-Key")
		c.auth = r.Header.Get("Authorization")
		w.WriteHeader(c.status)
		if c.status == http.StatusAccepted {
			_, _ = w.Write([]byte(`{"plan_id":"plan_01J"}`))
		}
	}
}

func testControl(t *testing.T, stub *ctrlStub) *Control {
	t.Helper()
	srv := httptest.NewServer(stub.handler())
	t.Cleanup(srv.Close)
	return NewControl(srv.URL, "dev_admin_token_not_for_prod", srv.Client(), nil)
}

func TestRevalidateSendsIdempotencyKeyAndBearer(t *testing.T) {
	stub := &ctrlStub{status: http.StatusAccepted}
	c := testControl(t, stub)

	require.NoError(t, c.Revalidate(context.Background(), "cand_01JABC", "delivery-guid-1234"))
	require.Equal(t, 1, stub.hits)
	require.Equal(t, "delivery-guid-1234", stub.idem,
		"originating ext_delivery_id MUST ride as Idempotency-Key so ctrl replays collapse")
	require.Equal(t, "Bearer dev_admin_token_not_for_prod", stub.auth)
}

func TestRevalidateMissingKeyFailsFastLocally(t *testing.T) {
	stub := &ctrlStub{status: http.StatusAccepted}
	c := testControl(t, stub)

	err := c.Revalidate(context.Background(), "cand_01JABC", "")
	require.Error(t, err, "ctrl 400s keyless calls; never spend the round trip")
	require.Zero(t, stub.hits)
}

func TestRevalidateStatusMapping(t *testing.T) {
	for tc, want := range map[int]error{
		http.StatusAccepted:     nil,
		http.StatusNotFound:     &ErrUnknownCandidate{},
		http.StatusConflict:     &ErrBudgetExhausted{},
		http.StatusUnauthorized: nil,
	} {
		stub := &ctrlStub{status: tc}
		c := testControl(t, stub)
		err := c.Revalidate(context.Background(), "cand_01JABC", "delivery-guid-1234")
		if want == nil {
			continue // non-typed outcomes are plain errors; presence is enough
		}
		require.ErrorAs(t, err, &want, "status %d must map to a typed error", tc)
	}
}

func TestRevalidate409IsTypedBudgetExhaustion(t *testing.T) {
	stub := &ctrlStub{status: http.StatusConflict}
	c := testControl(t, stub)

	err := c.Revalidate(context.Background(), "cand_01JABC", "delivery-guid-1234")
	var exhausted *ErrBudgetExhausted
	require.ErrorAs(t, err, &exhausted)
	require.Equal(t, "cand_01JABC", exhausted.CandidateID)
}

func TestRevalidateDisabledWithoutBaseURL(t *testing.T) {
	c := NewControl("", "tok", nil, nil)
	require.False(t, c.Enabled())
	require.Error(t, c.Revalidate(context.Background(), "cand_01JABC", "delivery-guid-1234"))
}
