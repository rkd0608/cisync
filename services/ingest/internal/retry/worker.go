// Package retry runs the internal delivery forward-retry loop.
package retry

import (
	"context"
	"encoding/json"
	"log/slog"
	"math"
	"time"

	"sauron.dev/sauron/ingest/internal/domain"
	"sauron.dev/sauron/ingest/internal/forward"
	"sauron.dev/sauron/ingest/internal/obs"
	"sauron.dev/sauron/ingest/internal/store"
)

// attemptsPerScan bounds the exp-backoff retry burst for one stuck delivery
// per scan pass (spec: 3 attempts, then forward_failed for reconciler pickup).
const attemptsPerScan = 3

// Worker scans deliveries stuck in pending or forward_failed older than 30s
// and re-forwards them with exponential backoff until the attempt budget is
// exhausted; exhausted deliveries are marked forward_failed for reconciler
// pickup.
type Worker struct {
	store       store.Store
	forwarder   *forward.Forwarder
	metrics     *obs.Metrics
	logger      *slog.Logger
	interval    time.Duration
	baseDelay   time.Duration
	maxAttempts int
	dueAge      time.Duration
	nowFn       func() time.Time
}

// NewWorker builds a retry worker. interval is the scan cadence, baseDelay the
// first exponential backoff step, nowFn the injectable clock.
func NewWorker(st store.Store, fw *forward.Forwarder, m *obs.Metrics, logger *slog.Logger, interval, baseDelay time.Duration, maxAttempts int, nowFn func() time.Time) *Worker {
	if nowFn == nil {
		nowFn = time.Now
	}
	return &Worker{
		store:       st,
		forwarder:   fw,
		metrics:     m,
		logger:      logger,
		interval:    interval,
		baseDelay:   baseDelay,
		maxAttempts: maxAttempts,
		dueAge:      30 * time.Second,
		nowFn:       nowFn,
	}
}

// Run blocks running scan passes until ctx is cancelled.
func (w *Worker) Run(ctx context.Context) {
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.ScanOnce(ctx)
		}
	}
}

// ScanOnce performs exactly one due-delivery scan pass.
func (w *Worker) ScanOnce(ctx context.Context) {
	w.scanOnce(ctx)
}

func (w *Worker) scanOnce(ctx context.Context) {
	deliveries, err := w.store.DueDeliveries(ctx, w.dueAge, w.maxAttempts, 100)
	if err != nil {
		w.logger.Error("retry scan failed", slog.String("err", err.Error()))
		return
	}
	for _, d := range deliveries {
		w.processOne(ctx, d)
		if ctx.Err() != nil {
			return
		}
	}
}

func (w *Worker) processOne(ctx context.Context, d domain.Delivery) {
	clean, err := forward.RedactPayload(d.Payload)
	if err != nil {
		w.markFailed(ctx, d, err)
		return
	}
	env := forward.Envelope{
		Source:           d.Source,
		ExtDeliveryID:    d.ExtDeliveryID,
		EventKind:        d.EventKind,
		Repo:             d.Repo,
		ReceivedAt:       d.ReceivedAt.UTC(),
		Payload:          json.RawMessage(clean),
		DuplicateSuspect: d.DuplicateSuspect,
	}

	result := forward.ResultUnavailable
	var cause error
	for attempt := 1; attempt <= attemptsPerScan; attempt++ {
		result, cause = w.forwarder.Send(ctx, env)
		w.metrics.CounterInc("ingest_retry_attempts_total", "Retry-worker forwarding attempts", "outcome", resultLabel(result))
		if result != forward.ResultUnavailable || ctx.Err() != nil {
			break
		}
		if attempt < attemptsPerScan {
			select {
			case <-ctx.Done():
				return
			case <-time.After(w.backoff(attempt)):
			}
		}
	}

	now := w.nowFn()
	attempts := d.Attempts + attemptsPerScan
	switch result {
	case forward.ResultAccepted:
		err := w.store.UpdateForwardState(ctx, d.ID, domain.StatusForwarded, attempts, now, now)
		if err != nil {
			w.logger.Error("mark forwarded failed", slog.String("delivery_id", d.ID), slog.String("err", err.Error()))
		}
		w.logger.Info("delivery forwarded on retry", slog.String("ext_delivery_id", d.ExtDeliveryID))
	case forward.ResultUnavailable:
		status := domain.StatusPending
		if attempts >= w.maxAttempts {
			status = domain.StatusForwardFailed
			w.logger.Warn("delivery exhausted retries",
				slog.String("ext_delivery_id", d.ExtDeliveryID), slog.Int("attempts", attempts))
		}
		err := w.store.UpdateForwardState(ctx, d.ID, status, attempts, now, time.Time{})
		if err != nil {
			w.logger.Error("mark retry state failed", slog.String("delivery_id", d.ID), slog.String("err", err.Error()))
		}
	default:
		w.markFailed(ctx, d, cause)
	}
}

func (w *Worker) markFailed(ctx context.Context, d domain.Delivery, cause error) {
	err := w.store.UpdateForwardState(ctx, d.ID, domain.StatusForwardFailed, d.Attempts+attemptsPerScan, w.nowFn(), time.Time{})
	if err != nil {
		w.logger.Error("mark forward_failed failed", slog.String("delivery_id", d.ID), slog.String("err", err.Error()))
	}
	w.logger.Warn("delivery marked forward_failed",
		slog.String("ext_delivery_id", d.ExtDeliveryID),
		slog.String("err", cause.Error()))
}

func (w *Worker) backoff(attempt int) time.Duration {
	d := time.Duration(float64(w.baseDelay) * math.Pow(2, float64(attempt-1)))
	if d > 30*time.Second {
		return 30 * time.Second
	}
	return d
}

func resultLabel(r forward.Result) string {
	switch r {
	case forward.ResultAccepted:
		return "accepted"
	case forward.ResultUnavailable:
		return "unavailable"
	default:
		return "rejected"
	}
}
