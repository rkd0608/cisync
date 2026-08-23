package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/oklog/ulid/v2"

	"sauron.dev/sauron/ingest/internal/domain"
	"sauron.dev/sauron/ingest/internal/forward"
	"sauron.dev/sauron/ingest/internal/obs"
	"sauron.dev/sauron/ingest/internal/store"
)

// retryAfterSeconds is the Retry-After hint returned when control-plane is
// unavailable so GitHub re-delivers.
const retryAfterSeconds = "30"

// maxInflight bounds concurrent forward attempts at the edge.
const maxInflight = 64

// GitHubHookHandler terminates POST /v1/hooks/github (T6).
type GitHubHookHandler struct {
	store     store.Store
	forwarder *forward.Forwarder
	metrics   *obs.Metrics
	logger    *slog.Logger
	nowFn     func() time.Time
	mu        sync.Mutex
	inflight  map[string]struct{}
	maxBody   int64
	skew      time.Duration
}

// NewGitHubHookHandler wires the webhook edge handler. maxBody is the payload
// size cap; skew is the tolerated timestamp drift.
func NewGitHubHookHandler(st store.Store, fw *forward.Forwarder, m *obs.Metrics, logger *slog.Logger, nowFn func() time.Time, maxBody int64, skew time.Duration) *GitHubHookHandler {
	return &GitHubHookHandler{
		store:     st,
		forwarder: fw,
		metrics:   m,
		logger:    logger,
		nowFn:     nowFn,
		inflight:  make(map[string]struct{}, maxInflight),
		maxBody:   maxBody,
		skew:      skew,
	}
}

// acquire marks a delivery as being processed; false means another request is
// currently handling the same (source, ext_delivery_id) pair.
func (h *GitHubHookHandler) acquire(extDeliveryID string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.inflight) >= maxInflight {
		return false
	}
	key := domain.SourceGitHub + "\x00" + extDeliveryID
	if _, busy := h.inflight[key]; busy {
		return false
	}
	h.inflight[key] = struct{}{}
	return true
}

func (h *GitHubHookHandler) release(extDeliveryID string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.inflight, domain.SourceGitHub+"\x00"+extDeliveryID)
}

// ServeHTTP implements the webhook edge protocol: 401 invalid signature,
// 413 oversized, 202 accepted on valid signature, and 503 + Retry-After when
// control-plane is unavailable at admission time (GitHub re-delivers).
func (h *GitHubHookHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	raw, err := readBody(w, r.Body, h.maxBody)
	if err != nil {
		reject(w, http.StatusRequestEntityTooLarge, "payload_too_large", "payload exceeds size cap", err)
		h.metrics.CounterInc("ingest_webhook_rejected_total", "Webhook requests rejected at the edge", "reason", "too_large")
		return
	}

	sigHeader := r.Header.Get("X-Hub-Signature-256")
	deliveryID := r.Header.Get("X-GitHub-Delivery")
	eventKind := r.Header.Get("X-GitHub-Event")
	if sigHeader == "" || !VerifyGitHubSignature(h.forwarder.Secret, raw, sigHeader) {
		h.metrics.CounterInc("ingest_webhook_rejected_total", "Webhook requests rejected at the edge", "reason", "bad_signature")
		h.logger.Warn("webhook signature verification failed",
			slog.String("ext_delivery_id", deliveryID), slog.String("event_kind", eventKind))
		http.Error(w, `{"error":{"code":"unauthorized","message":"invalid signature"}}`, http.StatusUnauthorized)
		return
	}
	if err := VerifyTimestampSkew(r.Header.Get("X-Sauron-Timestamp"), h.nowFn(), h.skew); err != nil {
		h.metrics.CounterInc("ingest_webhook_rejected_total", "Webhook requests rejected at the edge", "reason", "timestamp_skew")
		reject(w, http.StatusUnauthorized, "unauthorized", "stale or future timestamp", err)
		return
	}
	if deliveryID == "" || eventKind == "" {
		reject(w, http.StatusBadRequest, "validation_failed", "missing X-GitHub-Delivery or X-GitHub-Event", errors.New("api: missing github headers"))
		return
	}

	if !h.acquire(deliveryID) {
		h.metrics.CounterInc("ingest_webhook_deduped_total", "Duplicate deliveries collapsed by unique constraint")
		writeJSON(w, http.StatusAccepted, map[string]string{"status": "deduplicated"})
		return
	}
	defer h.release(deliveryID)

	d := domain.Delivery{
		ID:            "dlv_" + ulid.Make().String(),
		Source:        domain.SourceGitHub,
		ExtDeliveryID: deliveryID,
		EventKind:     eventKind,
		Repo:          extractRepo(raw),
		SigOK:         true,
		Headers: map[string]string{
			"x-github-event":      eventKind,
			"x-github-delivery":   deliveryID,
			"x-hub-signature-256": sigHeader,
			"content-type":        r.Header.Get("Content-Type"),
		},
		Payload:    raw,
		Status:     domain.StatusPending,
		ReceivedAt: h.nowFn(),
	}

	err = h.store.InsertDelivery(r.Context(), d)
	switch {
	case errors.Is(err, domain.ErrDuplicateDelivery):
		h.metrics.CounterInc("ingest_webhook_deduped_total", "Duplicate deliveries collapsed by unique constraint")
		writeJSON(w, http.StatusAccepted, map[string]string{"status": "deduplicated"})
		return
	case err != nil:
		h.logger.Error("persist delivery failed", slog.String("err", err.Error()))
		w.Header().Set("Retry-After", retryAfterSeconds)
		http.Error(w, `{"error":{"code":"unavailable","message":"persistence failure"}}`, http.StatusServiceUnavailable)
		return
	}

	result, ferr := h.forwardOne(r, d)
	h.recordOutcome(r.Context(), d, result, ferr)
	h.metrics.CounterInc("ingest_forward_attempts_total", "Control-plane forwarding attempts", "outcome", outcomeLabel(result))

	switch result {
	case forward.ResultAccepted:
		h.metrics.CounterInc("ingest_webhook_accepted_total", "Webhook requests accepted")
		writeJSON(w, http.StatusAccepted, map[string]string{"status": "accepted"})
	case forward.ResultUnavailable:
		h.logger.Warn("control-plane unavailable, deferring to retry worker",
			slog.String("ext_delivery_id", d.ExtDeliveryID))
		w.Header().Set("Retry-After", retryAfterSeconds)
		http.Error(w, `{"error":{"code":"unavailable","message":"control-plane unavailable, redeliver"}}`, http.StatusServiceUnavailable)
	default:
		h.logger.Warn("delivery forward rejected permanently",
			slog.String("ext_delivery_id", d.ExtDeliveryID), slog.String("err", ferr.Error()))
		h.metrics.CounterInc("ingest_webhook_accepted_total", "Webhook requests accepted")
		writeJSON(w, http.StatusAccepted, map[string]string{"status": "accepted_deferred"})
	}
}

// forwardOne performs a single redacted, signed forward attempt. The payload
// is scrubbed BEFORE anything leaves the process (T1).
func (h *GitHubHookHandler) forwardOne(r *http.Request, d domain.Delivery) (forward.Result, error) {
	clean, err := forward.RedactPayload(d.Payload)
	if err != nil {
		return forward.ResultRejected, err
	}
	env := forward.Envelope{
		Source:        d.Source,
		ExtDeliveryID: d.ExtDeliveryID,
		EventKind:     d.EventKind,
		Repo:          d.Repo,
		ReceivedAt:    d.ReceivedAt.UTC(),
		Payload:       json.RawMessage(clean),
	}
	return h.forwarder.Send(r.Context(), env)
}

// recordOutcome persists the single source of truth for forward state.
func (h *GitHubHookHandler) recordOutcome(ctx context.Context, d domain.Delivery, result forward.Result, cause error) {
	now := h.nowFn()
	var status string
	attempts := d.Attempts
	var forwardedAt time.Time
	switch result {
	case forward.ResultAccepted:
		status = domain.StatusForwarded
		forwardedAt = now
	case forward.ResultUnavailable:
		status = domain.StatusPending
		attempts++
	default:
		status = domain.StatusForwardFailed
		attempts++
	}
	err := h.store.UpdateForwardState(ctx, d.ID, status, attempts, now, forwardedAt)
	if err != nil {
		h.logger.Error("update delivery state failed",
			slog.String("delivery_id", d.ID), slog.String("err", err.Error()))
	}
	if cause != nil && status != domain.StatusForwarded {
		h.logger.Warn("delivery forward incomplete",
			slog.String("delivery_id", d.ID), slog.String("status", status), slog.String("err", cause.Error()))
	}
}

func extractRepo(raw []byte) string {
	var probe struct {
		Repository struct {
			FullName string `json:"full_name"`
		} `json:"repository"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		return ""
	}
	return probe.Repository.FullName
}

func outcomeLabel(r forward.Result) string {
	switch r {
	case forward.ResultAccepted:
		return "accepted"
	case forward.ResultUnavailable:
		return "unavailable"
	default:
		return "rejected"
	}
}

func reject(w http.ResponseWriter, code int, errCode string, message string, cause error) {
	if cause != nil {
		slog.Warn("webhook request rejected",
			slog.String("code", errCode), slog.String("err", cause.Error()))
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]any{"code": errCode, "message": message},
	})
}

func readBody(w http.ResponseWriter, r io.ReadCloser, capBytes int64) ([]byte, error) {
	buf, err := io.ReadAll(http.MaxBytesReader(w, r, capBytes))
	if err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			return nil, err
		}
		return nil, errors.New("api: body read failed")
	}
	return buf, nil
}

func writeJSON(w http.ResponseWriter, code int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(body)
}
