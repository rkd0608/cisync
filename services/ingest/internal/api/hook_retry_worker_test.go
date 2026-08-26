package api

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"testing"
	"time"

	"cisync.dev/cisync/ingest/internal/domain"
	"cisync.dev/cisync/ingest/internal/forward"
	"cisync.dev/cisync/ingest/internal/obs"
	"cisync.dev/cisync/ingest/internal/retry"
)

func TestRetryWorkerRecoversPendingDeliveries(t *testing.T) {
	var accepted atomicFlag
	h := newHarness(t, func(w http.ResponseWriter, r *http.Request) {
		if !accepted.get() {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		readEnvelope(t, r)
		w.WriteHeader(http.StatusAccepted)
	})

	resp := h.post("retry-1", "push", validPayload(), nil)
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("initial attempt must 503, got %d", resp.StatusCode)
	}

	fw := forward.New(h.ctrl.URL, []byte(h.secret))
	metrics := obs.New()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	worker := retry.NewWorker(h.st, fw, metrics, logger, time.Second, time.Millisecond, 10, func() time.Time { return h.currentTime() })

	h.advance(31 * time.Second)
	worker.ScanOnce(context.Background())
	accepted.set(true)
	h.advance(31 * time.Second)
	worker.ScanOnce(context.Background())

	d, err := h.st.GetDelivery(context.Background(), "github", "retry-1")
	if err != nil {
		t.Fatalf("delivery missing: %v", err)
	}
	if d.Status != domain.StatusForwarded {
		t.Fatalf("worker must eventually forward, status=%s attempts=%d", d.Status, d.Attempts)
	}
}

func TestRetryWorkerMarksExhaustedAsForwardFailed(t *testing.T) {
	h := newHarness(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	})
	h.post("exhaust-1", "push", validPayload(), nil)

	fw := forward.New(h.ctrl.URL, []byte(h.secret))
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	worker := retry.NewWorker(h.st, fw, obs.New(), logger, time.Second, time.Millisecond, 3, func() time.Time { return h.currentTime() })

	for i := 0; i < 4; i++ {
		h.advance(31 * time.Second)
		worker.ScanOnce(context.Background())
	}

	d, _ := h.st.GetDelivery(context.Background(), "github", "exhaust-1")
	if d.Status != domain.StatusForwardFailed {
		t.Fatalf("exhausted delivery must be forward_failed, got %s", d.Status)
	}
	if d.Attempts < 3 {
		t.Fatalf("attempts must reach max, got %d", d.Attempts)
	}
}
