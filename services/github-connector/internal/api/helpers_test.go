package api

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"cisync.dev/cisync/github-connector/internal/checks"
	"cisync.dev/cisync/github-connector/internal/emit"
	"cisync.dev/cisync/github-connector/internal/obs"
	"cisync.dev/cisync/github-connector/internal/rerun"
	"cisync.dev/cisync/github-connector/internal/tracking"
)

const testSecret = "test_conn_secret"

var frozenNow = time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)

// recordingRouter captures emit calls; dry-run posture by default.
type recordingRouter struct {
	creates   []checks.CheckPayload
	updates   []checks.CheckPayload
	updateIDs []int64
	nextID    int64
	err       error // injectable failure
}

func (r *recordingRouter) Create(_ context.Context, _ string, payload checks.CheckPayload) (emit.Result, error) {
	if r.err != nil {
		return emit.Result{}, r.err
	}
	r.creates = append(r.creates, payload)
	r.nextID++
	return emit.Result{CheckRunID: r.nextID, DryRun: true}, nil
}

func (r *recordingRouter) Update(_ context.Context, _ string, checkRunID int64, payload checks.CheckPayload) (emit.Result, error) {
	if r.err != nil {
		return emit.Result{}, r.err
	}
	r.updates = append(r.updates, payload)
	r.updateIDs = append(r.updateIDs, checkRunID)
	return emit.Result{CheckRunID: checkRunID}, nil
}

func (r *recordingRouter) InstallationFor(_ context.Context, _ string) (int64, bool) {
	return 42, true
}

// stubResolver answers every repo with installation 42 (or nothing).
type stubResolver struct{ known bool }

func (s stubResolver) ResolveInstallation(_ context.Context, _, _ string) (int64, error) {
	if !s.known {
		return 0, emit.ErrUnknownInstallation
	}
	return 42, nil
}

// harness bundles a fully wired §4 handler with recording seams.
type harness struct {
	router  *recordingRouter
	tracker *tracking.MemoryStore
	budget  *rerun.Budget
	control *rerun.Control
	seen    *rerun.Dedupe
	handler *DecisionsHandler
}

func newHarness(t *testing.T, policy rerun.Policy) *harness {
	t.Helper()
	h := &harness{
		router:  &recordingRouter{},
		tracker: tracking.NewMemoryStore(func() time.Time { return frozenNow }),
		budget:  rerun.NewBudget(2, 20, func() time.Time { return frozenNow }),
		seen:    rerun.NewDedupe(time.Hour, func() time.Time { return frozenNow }),
	}
	metrics := obs.New()
	logger := slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))
	// Control starts UNWIRED (feature-flag off); tests override h.control
	// when exercising the replan path.
	h.control = rerun.NewControl("", "", nil, func() time.Time { return frozenNow })
	h.handler = NewDecisionsHandler(testSecret, HandlerDeps{
		Tracker:      h.tracker,
		Router:       h.router,
		Metrics:      metrics,
		DetailsURL:   "http://localhost:3000",
		RerunPolicy:  policy,
		RerunBudget:  h.budget,
		RerunControl: h.control,
		RerunSeen:    h.seen,
		Now:          func() time.Time { return frozenNow },
	}, logger)
	return h
}

// withControl overrides the rerun control AFTER construction, patching the
// handler's deps too (mirrors how production wiring would swap it).
func (h *harness) withControl(c *rerun.Control) {
	h.control = c
	h.handler.deps.RerunControl = c
}

func (h *harness) post(t *testing.T, body any, idemKey string) *httptest.ResponseRecorder {
	t.Helper()
	raw, err := json.Marshal(body)
	require.NoError(t, err)
	req := httptest.NewRequest(http.MethodPost, "/internal/connector/decisions", bytes.NewReader(raw))
	req.Header.Set("X-CISync-Signature", signBody([]byte(testSecret), raw))
	req.Header.Set("Idempotency-Key", idemKey)
	rec := httptest.NewRecorder()
	h.handler.ServeHTTP(rec, req)
	return rec
}
