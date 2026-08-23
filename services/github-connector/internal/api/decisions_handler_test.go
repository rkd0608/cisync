package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"sauron.dev/sauron/github-connector/internal/domain"
	"sauron.dev/sauron/github-connector/internal/obs"
	"sauron.dev/sauron/github-connector/internal/store"
)

const testSecret = "test_conn_secret"

type testServer struct {
	srv   *httptest.Server
	store *store.MemoryStore
	pub   *recordingPublisher
}

func newTestServer(t *testing.T) *testServer {
	t.Helper()
	h := &testServer{store: store.NewMemoryStore(nil), pub: &recordingPublisher{}}
	mux := http.NewServeMux()
	mux.Handle("POST /internal/connector/decisions", NewDecisionsHandler(
		h.store, h.pub, obs.New(), slog.New(slog.NewTextHandler(io.Discard, nil)),
		testSecret, "http://localhost:3000", true))
	h.srv = httptest.NewServer(mux)
	t.Cleanup(h.srv.Close)
	return h
}

func validEnvelope() domain.DecisionEnvelope {
	return domain.DecisionEnvelope{
		DecisionID:  "dec_01JTESTDECISION",
		CandidateID: "cand_01JTESTCANDIDATE",
		Repo:        "acme/payments",
		HeadSHA:     "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Verb:        domain.VerbRejected,
		Confidence:  0.91,
		Policy:      domain.PolicyRef{PolicyID: "pol_sauron_default", Version: 1},
		Summary:     "deterministic regression",
		RenderedAt:  time.Now().UTC(),
	}
}

func TestDecisionsHMACEnforced(t *testing.T) {
	h := newTestServer(t)
	raw, _ := json.Marshal(validEnvelope())

	req, _ := http.NewRequest(http.MethodPost, h.srv.URL+"/internal/connector/decisions", bytes.NewReader(raw))
	req.Header.Set("X-Sauron-Signature", signBody([]byte("wrong"), raw))
	req.Header.Set("Idempotency-Key", "dec_01JTESTDECISION")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusUnauthorized, resp.StatusCode)

	_, err = h.store.GetCheckReport(context.Background(), "dec_01JTESTDECISION")
	require.ErrorIs(t, err, store.ErrNotFound, "rejected pushes must not persist")
	require.Empty(t, h.pub.payloads, "rejected pushes must never publish")
}

func TestDecisionsDryRunAcceptsAndPersists(t *testing.T) {
	h := newTestServer(t)
	env := validEnvelope()
	raw, _ := json.Marshal(env)
	req, _ := http.NewRequest(http.MethodPost, h.srv.URL+"/internal/connector/decisions", bytes.NewReader(raw))
	req.Header.Set("X-Sauron-Signature", signBody([]byte(testSecret), raw))
	req.Header.Set("Idempotency-Key", env.DecisionID)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusAccepted, resp.StatusCode)

	var body map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	require.Equal(t, true, body["accepted"])
	require.Equal(t, true, body["dry_run"])

	rep, err := h.store.GetCheckReport(context.Background(), env.DecisionID)
	require.NoError(t, err)
	require.Equal(t, "failure", rep.Conclusion)
	require.True(t, rep.DryRun)

	resp2, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp2.Body.Close()
	require.Equal(t, http.StatusOK, resp2.StatusCode, "replay is idempotent 200")
}

func TestDecisionsValidationFailures(t *testing.T) {
	h := newTestServer(t)
	post := func(env domain.DecisionEnvelope) *http.Response {
		raw, _ := json.Marshal(env)
		req, _ := http.NewRequest(http.MethodPost, h.srv.URL+"/internal/connector/decisions", bytes.NewReader(raw))
		req.Header.Set("X-Sauron-Signature", signBody([]byte(testSecret), raw))
		req.Header.Set("Idempotency-Key", env.DecisionID)
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		return resp
	}

	bad := validEnvelope()
	bad.Verb = "combine"
	resp := post(bad)
	resp.Body.Close()
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)

	bad = validEnvelope()
	bad.HeadSHA = "short"
	resp = post(bad)
	resp.Body.Close()
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)

	bad = validEnvelope()
	bad.DecisionID = "not_a_ulid"
	resp = post(bad)
	resp.Body.Close()
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)

	mismatched := validEnvelope()
	rawMismatched, _ := json.Marshal(mismatched)
	req, _ := http.NewRequest(http.MethodPost, h.srv.URL+"/internal/connector/decisions", bytes.NewReader(rawMismatched))
	req.Header.Set("X-Sauron-Signature", signBody([]byte(testSecret), rawMismatched))
	req.Header.Set("Idempotency-Key", "dec_other")
	respM, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	respM.Body.Close()
	require.Equal(t, http.StatusBadRequest, respM.StatusCode)
}
