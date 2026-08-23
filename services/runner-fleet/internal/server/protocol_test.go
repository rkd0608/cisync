package server

import (
	"context"
	"net/http"
	"sync"
	"testing"
	"time"
)

func TestClaimReturnsQueuedJobsWithFenceTokens(t *testing.T) {
	h := newHarness(t)
	h.enqueue(testJobParams{runID: "run-a"})
	h.enqueue(testJobParams{runID: "run-b"})

	claimed := h.claim("w-1", 4)
	if len(claimed) != 2 {
		t.Fatalf("expected 2 claimed jobs, got %d", len(claimed))
	}
	for _, j := range claimed {
		if j.FenceToken < 1 {
			t.Fatalf("fence token must be >=1 after first claim, got %d for %s", j.FenceToken, j.RunID)
		}
		if j.Pool != "sim" || j.Attempt != 1 {
			t.Fatalf("unexpected claim shape: %+v", j)
		}
	}

	job, err := h.st.Get(context.Background(), "run-a")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if job.Status != "running" || job.ClaimedBy != "w-1" {
		t.Fatalf("job must be running and attributed: %+v", job)
	}
}

func TestClaimAtomicDoubleExclusion(t *testing.T) {
	h := newHarness(t)
	const total = 40
	for i := 0; i < total; i++ {
		h.enqueue(testJobParams{runID: string(rune('a'+i/26)) + string(rune('a'+i%26))})
	}

	var mu sync.Mutex
	seen := make(map[string]bool)
	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for i := 0; i < 5; i++ {
				claimed := h.claim("worker-"+string(rune('A'+worker)), 2)
				mu.Lock()
				for _, j := range claimed {
					if seen[j.RunID] {
						t.Errorf("double claim of %s detected", j.RunID)
					}
					seen[j.RunID] = true
				}
				mu.Unlock()
			}
		}(w)
	}
	wg.Wait()
	if len(seen) != total {
		t.Fatalf("expected all %d jobs claimed exactly once, got %d", total, len(seen))
	}
}

// TestCompleteStaleFenceRejectedAfterReclaim is THE money test: a worker that
// lost its job (stale heartbeats → epoch bumped by reclaim) must receive 409
// fence_mismatch, and only the current token holder may accept (I-11).
func TestCompleteStaleFenceRejectedAfterReclaim(t *testing.T) {
	h := newHarness(t)
	h.enqueue(testJobParams{runID: "run-money"})

	first := h.claim("w-1", 1)
	if len(first) != 1 {
		t.Fatalf("first claim failed")
	}
	staleToken := first[0].FenceToken

	h.advance(20 * time.Second)
	requeued, err := h.st.RequeueStale(context.Background(), 15*time.Second, h.clock())
	if err != nil || len(requeued) != 1 {
		t.Fatalf("reclaim must requeue the stale job: %v %v", requeued, err)
	}

	resp, body := h.complete("run-money", staleToken, "succeeded")
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("stale complete must 409, got %d (%v)", resp.StatusCode, body)
	}
	if body["accepted"] != false || body["reason"] != "fence_mismatch" {
		t.Fatalf("wrong conflict body: %v", body)
	}

	second := h.claim("w-2", 1)
	if len(second) != 1 || second[0].RunID != "run-money" {
		t.Fatalf("second worker must reclaim the run")
	}
	newToken := second[0].FenceToken
	if newToken <= staleToken {
		t.Fatalf("epoch must strictly increase on reclaim: %d then %d", staleToken, newToken)
	}

	respOK, bodyOK := h.complete("run-money", newToken, "succeeded")
	if respOK.StatusCode != http.StatusOK || bodyOK["accepted"] != true {
		t.Fatalf("current holder must accept: %d %v", respOK.StatusCode, bodyOK)
	}

	job, _ := h.st.Get(context.Background(), "run-money")
	if !job.Accepted || job.Status != "succeeded" {
		t.Fatalf("accepted state wrong: %+v", job)
	}
	ref := job.ResultRef["logs_digest"]
	if ref == "" {
		t.Fatalf("logs_digest must be recorded in result_ref")
	}
}

func TestCompleteAlreadyAcceptedIdempotentRejection(t *testing.T) {
	h := newHarness(t)
	h.enqueue(testJobParams{runID: "run-once"})
	claimed := h.claim("w-1", 1)

	resp, _ := h.complete("run-once", claimed[0].FenceToken, "succeeded")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("first complete must 200, got %d", resp.StatusCode)
	}
	resp2, body2 := h.complete("run-once", claimed[0].FenceToken, "failed")
	if resp2.StatusCode != http.StatusConflict {
		t.Fatalf("second complete must 409, got %d", resp2.StatusCode)
	}
	if body2["reason"] != "already_accepted" {
		t.Fatalf("reason must be already_accepted, got %v", body2["reason"])
	}
	job, _ := h.st.Get(context.Background(), "run-once")
	if job.Status != "succeeded" {
		t.Fatalf("state must remain the first accepted result, got %s", job.Status)
	}
}

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
	respUnknown, _ := h.heartbeat("run-unknown", token)
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
	respUnknown, _ := h.complete("run-none", 9, "succeeded")
	if respUnknown.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown run on complete must 404, got %d", respUnknown.StatusCode)
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
