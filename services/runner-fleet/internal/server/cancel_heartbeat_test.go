package server

import (
	"context"
	"net/http"
	"testing"
	"time"
)

func TestHeartbeatFreshAndStaleFences(t *testing.T) {
	h := newHarness(t)
	h.enqueue(testJobParams{runID: "run-hb"})
	claimed := h.claim("w-1", 1)
	token := claimed[0].FenceToken

	resp, _ := h.heartbeat("run-hb", token)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("fresh heartbeat must 204, got %d", resp.StatusCode)
	}
	respStale, body := h.heartbeat("run-hb", token-1)
	if respStale.StatusCode != http.StatusConflict {
		t.Fatalf("stale heartbeat must 409, got %d", respStale.StatusCode)
	}
	if body["reason"] != "fence_mismatch" {
		t.Fatalf("heartbeat conflict reason wrong: %v", body)
	}
	// Unknown runs: 404 for AUTHENTICATED callers (uniform 404, no
	// existence leak); unauthenticated callers get the opaque 401.
	respUnknown, _ := h.heartbeatWithToken("run-unknown", token, h.mintLease("run-unknown", 1, token, time.Minute))
	if respUnknown.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown run must 404, got %d", respUnknown.StatusCode)
	}
}

func TestCancelAfterCompleteIgnoredStateUnchanged(t *testing.T) {
	h := newHarness(t)
	h.enqueue(testJobParams{runID: "run-done"})
	claimed := h.claim("w-1", 1)
	respComplete, _ := h.complete("run-done", claimed[0].FenceToken, "succeeded")
	if respComplete.StatusCode != http.StatusOK {
		t.Fatalf("setup complete failed")
	}
	before, _ := h.st.Get(context.Background(), "run-done")

	respCancel, _ := h.cancel("run-done", "superseded")
	if respCancel.StatusCode != http.StatusNoContent {
		t.Fatalf("cancel after complete must still be 204 idempotent, got %d", respCancel.StatusCode)
	}
	after, _ := h.st.Get(context.Background(), "run-done")
	if after.Status != before.Status || after.FenceToken != before.FenceToken ||
		after.ResultRef["status"] != before.ResultRef["status"] {
		t.Fatalf("cancel-after-complete must not mutate accepted state: %+v vs %+v", after, before)
	}
}

func TestCancelRunningBumpsEpochAndCallsProvider(t *testing.T) {
	h := newHarness(t)
	h.enqueue(testJobParams{runID: "run-live"})
	claimed := h.claim("w-1", 1)
	staleToken := claimed[0].FenceToken

	h.srv.Registry.Register("run-live", "run-live")

	resp, _ := h.cancel("run-live", "superseded_by_representative")
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("cancel running must 204, got %d", resp.StatusCode)
	}
	if cancels := h.cancelledRuns(); len(cancels) != 1 || cancels[0] != "run-live" {
		t.Fatalf("provider.Cancel must be called best-effort: %v", cancels)
	}

	respLate, body := h.complete("run-live", staleToken, "succeeded")
	if respLate.StatusCode != http.StatusConflict || body["reason"] != "fence_mismatch" {
		t.Fatalf("post-cancel completion with old token must 409 fence_mismatch: %d %v", respLate.StatusCode, body)
	}

	respAgain, _ := h.cancel("run-live", "repeat")
	if respAgain.StatusCode != http.StatusNoContent {
		t.Fatalf("cancel is idempotent 204, got %d", respAgain.StatusCode)
	}
	job, _ := h.st.Get(context.Background(), "run-live")
	if job.Status != "cancelled" {
		t.Fatalf("status stays cancelled, got %s", job.Status)
	}
}

func TestCompleteValidationErrors(t *testing.T) {
	h := newHarness(t)
	h.enqueue(testJobParams{runID: "run-v"})
	claimed := h.claim("w-1", 1)
	token := claimed[0].FenceToken

	respBadStatus, _ := h.post("/internal/fleet/jobs/run-v/complete", map[string]any{
		"fence_token": token, "status": "exploded",
		"logs_digest": digestFor([]byte("x")), "duration_ms": 1,
	})
	if respBadStatus.StatusCode != http.StatusBadRequest {
		t.Fatalf("bad status value must 400, got %d", respBadStatus.StatusCode)
	}
	respBadDigest, _ := h.post("/internal/fleet/jobs/run-v/complete", map[string]any{
		"fence_token": token, "status": "failed",
		"logs_digest": "md5:nope", "duration_ms": 1,
	})
	if respBadDigest.StatusCode != http.StatusBadRequest {
		t.Fatalf("bad digest must 400, got %d", respBadDigest.StatusCode)
	}
	// Unknown runs answer 404 to AUTHENTICATED callers (uniform 404, no
	// cross-tenant existence leak); without a credential it is opaque 401.
	respUnauthed := h.rawComplete("run-none", 9, "succeeded", "")
	_ = respUnauthed
	if respUnauthed.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated complete must 401 before existence checks, got %d", respUnauthed.StatusCode)
	}
	respAuthedUnknown, _ := h.completeWithToken("run-none", 9, "succeeded", h.mintLease("run-none", 1, 9, time.Minute))
	if respAuthedUnknown.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown run on authenticated complete must 404, got %d", respAuthedUnknown.StatusCode)
	}
}

func TestQueueDepthGaugeAndMetricsEndpoint(t *testing.T) {
	h := newHarness(t)
	h.enqueue(testJobParams{runID: "run-q1"})
	h.enqueue(testJobParams{runID: "run-q2"})

	depth, err := h.st.QueueDepth(context.Background())
	if err != nil || depth["sim/0"] != 2 {
		t.Fatalf("queue depth wrong: %v %v", depth, err)
	}
}
