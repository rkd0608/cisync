package server

import (
	"context"
	"net/http"
	"testing"
)

// P0-2 / I-01: the outcome census rides the completion payload, is stored in
// result_ref with a digest, and reaches the control-plane feed.

func TestCompleteCarriesOutcomeCensusIntoResultRef(t *testing.T) {
	h := newHarness(t)
	job := h.enqueue(testJobParams{runID: "run-census"})
	claimed := h.claim("w-1", 1)

	resp, body := h.completeCensus(job.RunID, claimed[0].FenceToken, job.LeaseToken, map[string]any{
		"total": 10, "passed": 7, "failed": 0, "skipped": 3, "quarantined": 0,
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("setup complete failed: %d %v", resp.StatusCode, body)
	}
	stored, err := h.st.Get(context.Background(), job.RunID)
	if err != nil {
		t.Fatalf("load job: %v", err)
	}
	results, ok := stored.ResultRef["results"].(map[string]any)
	if !ok {
		t.Fatalf("result_ref must embed the results census: %+v", stored.ResultRef)
	}
	if results["total"].(float64) != 10 || results["passed"].(float64) != 7 || results["skipped"].(float64) != 3 {
		t.Fatalf("census values wrong: %+v", results)
	}
	digest, _ := stored.ResultRef["results_digest"].(string)
	if len(digest) != len("sha256:")+64 {
		t.Fatalf("results_digest must be recorded, got %q", digest)
	}

	// The census must survive to the completion feed (§4) for ctrl-side
	// I-01 validation.
	feed, err := h.st.TerminalAccepted(context.Background(), 10)
	if err != nil || len(feed) == 0 {
		t.Fatalf("terminal feed must include the completed job: %v", err)
	}
	if feed[0].ResultRef["results"] == nil {
		t.Fatal("feed row must carry the census")
	}
}

func TestCompleteRejectsInconsistentCensus(t *testing.T) {
	h := newHarness(t)
	job := h.enqueue(testJobParams{runID: "run-bad-census"})
	claimed := h.claim("w-1", 1)

	resp, _ := h.completeCensus(job.RunID, claimed[0].FenceToken, job.LeaseToken, map[string]any{
		"total": 5, "passed": 9,
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("inconsistent census must 400, got %d", resp.StatusCode)
	}
}
