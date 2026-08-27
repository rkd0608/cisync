package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"cisync.dev/cisync/github-connector/internal/domain"
	"cisync.dev/cisync/github-connector/internal/obs"
	"cisync.dev/cisync/github-connector/internal/rerun"
)

// recordingReporter captures sticky-report posts and can inject failure.
type recordingReporter struct {
	calls []domain.DecisionEnvelope
	err   error
}

func (r *recordingReporter) Post(_ context.Context, env *domain.DecisionEnvelope) error {
	if r.err != nil {
		return r.err
	}
	r.calls = append(r.calls, *env)
	return nil
}

func reportEnvelope(prNumber int) domain.DecisionEnvelope {
	env := decisionEnvelopeFor("cand_01JREP", "ffffffffffffffffffffffffffffffffffffffff", "dec_01JREPORT")
	env.PRNumber = prNumber
	return env
}

// skipCounter reads the cisync_report_skipped_total sample for one reason;
// absent series ⇒ 0 so assertions stay linear.
func skipCounter(t *testing.T, metrics *obs.Metrics, reason string) int {
	t.Helper()
	want := "cisync_report_skipped_total{reason=\"" + reason + "\"}"
	for _, line := range strings.Split(metrics.Render(), "\n") {
		if strings.HasPrefix(line, want+" ") || line == want+" 0" ||
			strings.HasPrefix(line, want) && !strings.Contains(line, "#") {
			var v float64
			value := strings.TrimSpace(strings.TrimPrefix(line, want))
			if value == "" {
				continue
			}
			if err := json.Unmarshal([]byte(value), &v); err == nil {
				return int(v)
			}
		}
	}
	return 0
}

// TestDecisionPushesStickyReport proves the happy path: a live decision push
// carrying pr_number delegates exactly one Post to the reporter.
func TestDecisionPushesStickyReport(t *testing.T) {
	h := newHarness(t, rerun.PolicyReplan)
	h.router.liveMode = true
	reporter := &recordingReporter{}
	h.handler.deps.Reporter = reporter

	resp := h.post(t, reportEnvelope(17), "dec_01JREPORT")
	require.Equal(t, http.StatusAccepted, resp.Code)
	require.Len(t, reporter.calls, 1)
	require.Equal(t, 17, reporter.calls[0].PRNumber)
	require.Zero(t, skipCounter(t, h.handler.deps.Metrics, "no_pr_number"))
}

// TestDecisionSkipsStickyReport covers every silent-skip reason with its
// metric label, plus the never-break-the-push failure contract.
func TestDecisionSkipsStickyReport(t *testing.T) {
	t.Run("no_pr_number_on_live_push", func(t *testing.T) {
		h := newHarness(t, rerun.PolicyReplan)
		h.router.liveMode = true
		h.handler.deps.Reporter = &recordingReporter{}

		resp := h.post(t, reportEnvelope(0), "dec_01JREPORT")
		require.Equal(t, http.StatusAccepted, resp.Code)
		require.Zero(t, len(h.handler.deps.Reporter.(*recordingReporter).calls))
		require.Equal(t, 1, skipCounter(t, h.handler.deps.Metrics, "no_pr_number"))
	})

	t.Run("dry_run", func(t *testing.T) {
		h := newHarness(t, rerun.PolicyReplan)
		h.handler.deps.Reporter = &recordingReporter{}

		resp := h.post(t, reportEnvelope(17), "dec_01JREPORT")
		require.Equal(t, http.StatusAccepted, resp.Code)
		require.Zero(t, len(h.handler.deps.Reporter.(*recordingReporter).calls))
		require.Equal(t, 1, skipCounter(t, h.handler.deps.Metrics, "dry_run"))
	})

	t.Run("write_queued", func(t *testing.T) {
		h := newHarness(t, rerun.PolicyReplan)
		h.router.liveMode = true
		h.router.queuedMode = true
		h.handler.deps.Reporter = &recordingReporter{}

		resp := h.post(t, reportEnvelope(17), "dec_01JREPORT")
		require.Equal(t, http.StatusAccepted, resp.Code)
		require.Zero(t, len(h.handler.deps.Reporter.(*recordingReporter).calls))
		require.Equal(t, 1, skipCounter(t, h.handler.deps.Metrics, "write_queued"))
	})

	t.Run("reporter_failure_never_breaks_the_push", func(t *testing.T) {
		h := newHarness(t, rerun.PolicyReplan)
		h.router.liveMode = true
		h.handler.deps.Reporter = &recordingReporter{err: errors.New("github down")}

		resp := h.post(t, reportEnvelope(17), "dec_01JREPORT")
		require.Equal(t, http.StatusAccepted, resp.Code,
			"comment surface is best-effort; the gate verdict stands on its own")
	})
}

// TestDecisionWithoutReporterIsQuiet proves feature-OFF deployments emit no
// skipped-comment metric noise at all.
func TestDecisionWithoutReporterIsQuiet(t *testing.T) {
	h := newHarness(t, rerun.PolicyReplan)
	h.router.liveMode = true

	resp := h.post(t, reportEnvelope(17), "dec_01JREPORT")
	require.Equal(t, http.StatusAccepted, resp.Code)
	require.NotContains(t, h.handler.deps.Metrics.Render(), "cisync_report_skipped_total")
}
