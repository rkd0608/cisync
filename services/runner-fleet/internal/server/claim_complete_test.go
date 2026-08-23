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
