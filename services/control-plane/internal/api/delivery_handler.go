package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/jackc/pgx/v5"

	"sauron.dev/sauron/control-plane/internal/domain"
	"sauron.dev/sauron/control-plane/internal/store"
)

// buildDeliveryAcceptedEvent constructs the delivery.accepted CORE event.
// The aggregate id is a platform-minted dlv_ ULID (events.schema.json
// prefixedUlid); the external GitHub GUID stays only in payload.ext_delivery_id.
// normalized MUST be part of the initial payload map: the payload digest is
// computed inside NewEvent, so ANY post-construction mutation breaks I-07
// verification of the served ledger (regression caught in W5 smoke).
func buildDeliveryAcceptedEvent(tenantID string, body *deliveryBody, normalized string) (*domain.Event, error) {
	return domain.NewEvent(tenantID,
		domain.AggregateRef{Type: string(domain.AggDelivery), ID: domain.NewID(domain.PrefixDelivery)},
		"delivery.accepted", "", "", domain.EventActor{Kind: string(domain.ActorGitHub), ID: "github"},
		map[string]any{
			"source":          body.Source,
			"ext_delivery_id": body.ExtDeliveryID,
			"normalized_kind": normalized,
			"repo":            body.Repo,
		})
}

// delivery_body.go holds the §1 wire type; see delivery_audit_seams.go for
// the additive flag fields' rationale.

// handleDelivery implements POST /internal/ctrl/deliveries (ingest →
// control-plane webhook forwarding, internal-protocols §1).
func (s *Server) handleDelivery(w http.ResponseWriter, r *http.Request) {
	raw, ok := s.readRawBody(w, r)
	if !ok {
		return
	}
	sig := r.Header.Get("X-Sauron-Signature")
	if !verifyHMAC(s.cfg.WebhookSecret, raw, sig) {
		WriteError(w, http.StatusUnauthorized, "unauthorized", "bad signature", nil, nil, nil)
		s.metrics.Inc("sauron_ctrl_http_requests_total", "401")
		return
	}
	extID := r.Header.Get("Idempotency-Key")
	if extID == "" {
		WriteError(w, http.StatusBadRequest, "validation_failed", "Idempotency-Key (ext delivery id) required", nil, nil, nil)
		return
	}
	if s.store == nil {
		// Store-less constructions (hermetic tests) must fail closed AFTER
		// signature checks so auth failures still audit, but never panic.
		WriteError(w, http.StatusServiceUnavailable, "unavailable", "storage unavailable; redeliver", nil, nil, nil)
		return
	}
	var body deliveryBody
	if err := json.Unmarshal(raw, &body); err != nil {
		WriteError(w, http.StatusBadRequest, "validation_failed", "invalid delivery JSON", nil, nil, nil)
		return
	}

	var payload map[string]any
	if len(body.Payload) > 0 {
		if err := json.Unmarshal(body.Payload, &payload); err != nil || payload == nil {
			payload = map[string]any{}
		}
	} else {
		payload = map[string]any{}
	}

	cached, _ := s.store.LookupCommand(r.Context(), s.cfg.TenantID, "POST /internal/ctrl/deliveries", extID, requestHash(raw))
	if cached != nil && cached.ResponseCode != 0 {
		// internal-protocols §1: replays answer 200 regardless of the
		// originally recorded accept code.
		writeRawJSON(w, http.StatusOK, []byte(`{"accepted":true,"replay":true}`))
		s.metrics.Inc("sauron_ctrl_http_requests_total", "200")
		return
	}

	// Audit-only envelopes (B7 sig-failure markers, H2 duplicate suspects)
	// record diagnostics WITHOUT domain effects — a suspected near-replay
	// or a rejected-signature delivery must never mutate state.
	if body.QuarantineReason != "" {
		code, respBody, err := s.applyQuarantineMarker(r.Context(), requestHash(raw), &body)
		if err != nil {
			logError("sig-failed marker %s failed: %v", extID, err)
			retry := 5
			WriteError(w, http.StatusServiceUnavailable, "unavailable", "storage unavailable; redeliver",
				nil, &retry, nil)
			w.Header().Set("Retry-After", "5")
			return
		}
		writeRawJSON(w, code, respBody)
		s.metrics.Inc("sauron_ctrl_http_requests_total", fmt.Sprint(code))
		return
	}
	if body.DuplicateSuspect {
		code, respBody, err := s.applyDuplicateSuspect(r.Context(), &body, requestHash(raw))
		if err != nil {
			logError("duplicate-suspect delivery %s failed: %v", extID, err)
			retry := 5
			WriteError(w, http.StatusServiceUnavailable, "unavailable", "storage unavailable; redeliver",
				nil, &retry, nil)
			w.Header().Set("Retry-After", "5")
			return
		}
		writeRawJSON(w, code, respBody)
		s.metrics.Inc("sauron_ctrl_http_requests_total", fmt.Sprint(code))
		return
	}

	view := normalizeDelivery(body.EventKind, body.Repo, payload, s.cfg.TrackedBaseBranches)
	ev, err := buildDeliveryAcceptedEvent(s.cfg.TenantID, &body, normalizedLabel(view))
	if err != nil {
		WriteDomainError(w, err)
		return
	}
	// The delivery.accepted event carries the normalized kind so the ledger
	// tail shows the §3.1 mapping without replaying payloads.

	var responseCode int
	responseBody := []byte(`{"accepted":true}`)
	err = s.store.ExecTx(r.Context(), func(tx pgx.Tx) error {
		// I-12: exactly-once effects keyed by ext_delivery_id INSIDE the
		// effect tx — at-least-once forwarding never double-submits.
		fresh, err := store.MarkProcessedTx(r.Context(), tx, "delivery-normalizer", extID)
		if err != nil {
			return err
		}
		if !fresh {
			responseCode = http.StatusOK
			responseBody = []byte(`{"accepted":true,"replay":true}`)
			return nil
		}
		// Batch 1: the delivery.accepted anchor. Effect events append in
		// their own batches because their projections depend on appended
		// seqs; webhook rates are orders of magnitude below the storm paths
		// the single-batch rule exists for.
		if err := s.store.AppendEventsTx(r.Context(), tx, []*domain.Event{ev}); err != nil {
			return err
		}
		s.metrics.Add("sauron_ctrl_events_appended_total", 1)
		if err := s.applyDeliveryEffects(r.Context(), tx, &body, view); err != nil {
			return err
		}
		if err := store.RecordCommandTx(r.Context(), tx, s.cfg.TenantID,
			"POST /internal/ctrl/deliveries", extID, requestHash(raw), http.StatusAccepted,
			[]byte(`{"accepted":true}`)); err != nil {
			return err
		}
		responseCode = http.StatusAccepted
		return nil
	})
	if err != nil {
		logError("delivery %s effect tx failed: %v", extID, err)
		retry := 5
		WriteError(w, http.StatusServiceUnavailable, "unavailable", "storage unavailable; redeliver",
			nil, &retry, nil)
		w.Header().Set("Retry-After", "5")
		return
	}
	writeRawJSON(w, responseCode, responseBody)
	s.metrics.Inc("sauron_ctrl_http_requests_total", fmt.Sprint(responseCode))
}

// applyDeliveryEffects projects §3.1 ledger effects for one delivery inside
// the caller's transaction. Unknown kinds park record-only (never 4xx).
func (s *Server) applyDeliveryEffects(ctx context.Context, tx pgx.Tx, body *deliveryBody, view deliveryView) error {
	tenant := s.cfg.TenantID
	extID := body.ExtDeliveryID
	switch view.Kind {
	case KindPROpened:
		return s.applyPROpened(ctx, tx, tenant, view)
	case KindPRSynchronize:
		return s.applyPRSynchronize(ctx, tx, tenant, extID, view)
	case KindPRClosed:
		return s.applyPRClosed(ctx, tx, tenant, extID, view)
	case KindPushBaseAdv:
		return s.applyPushBaseAdvanced(ctx, tx, tenant, extID, view)
	case KindInstallDeleted:
		return s.applyInstallationDeleted(ctx, tx, tenant, view)
	default:
		// push.branch / installation.created / permissions_changed /
		// check_run.rerequested / unknown.* stay delivery.accepted-only.
		s.metrics.Add("sauron_ctrl_unknown_event_total", 1, normalizedLabel(view))
		return nil
	}
}

// normalizedLabel returns the ledger-facing kind label (unknown parks as
// "unknown.<event>[.<action>]").
func normalizedLabel(view deliveryView) string {
	if view.Kind == "" && view.Raw != "" {
		return view.Raw
	}
	return string(view.Kind)
}

// handleGitHubWebhook implements POST /v1/hooks/github (openapi CORE #11);
// valid signature ⇒ always 202, async processing downstream.
func (s *Server) handleGitHubWebhook(w http.ResponseWriter, r *http.Request) {
	raw, ok := s.readRawBody(w, r)
	if !ok {
		return
	}
	for _, h := range []string{"X-Hub-Signature-256", "X-GitHub-Delivery", "X-GitHub-Event"} {
		if r.Header.Get(h) == "" {
			WriteError(w, http.StatusUnauthorized, "unauthorized", "missing "+h, nil, nil, nil)
			return
		}
	}
	if !verifyHMAC(s.cfg.WebhookSecret, raw, r.Header.Get("X-Hub-Signature-256")) {
		// B7: a signature failure on the direct GitHub alias route is the
		// same security event as an ingest-quarantined delivery.
		s.auditWebhookSignatureFailure(r.Header.Get("X-GitHub-Delivery"), r.Header.Get("X-GitHub-Event"))
		WriteError(w, http.StatusUnauthorized, "unauthorized", "bad signature", nil, nil, nil)
		s.metrics.Inc("sauron_ctrl_http_requests_total", "401")
		return
	}
	body := deliveryBody{
		Source:        "github",
		ExtDeliveryID: r.Header.Get("X-GitHub-Delivery"),
		EventKind:     r.Header.Get("X-GitHub-Event"),
	}
	ev, err := buildDeliveryAcceptedEvent(s.cfg.TenantID, &body, body.EventKind)
	if err != nil {
		WriteDomainError(w, err)
		return
	}
	err = s.store.ExecTx(r.Context(), func(tx pgx.Tx) error {
		err := s.store.AppendEventsTx(r.Context(), tx, []*domain.Event{ev})
		if err == nil {
			s.metrics.Add("sauron_ctrl_events_appended_total", 1)
		}
		return err
	})
	if err != nil {
		WriteDomainError(w, err)
		return
	}
	writeRawJSON(w, http.StatusAccepted, []byte(`{"accepted":true}`))
	s.metrics.Inc("sauron_ctrl_http_requests_total", "202")
}
