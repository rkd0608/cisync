package api

import (
	"encoding/json"
	"net/http"

	"github.com/jackc/pgx/v5"

	"sauron.dev/sauron/control-plane/internal/domain"
	"sauron.dev/sauron/control-plane/internal/store"
)

// buildDeliveryAcceptedEvent constructs the delivery.accepted CORE event.
// The aggregate id is a platform-minted dlv_ ULID (events.schema.json
// prefixedUlid); the external GitHub GUID stays only in payload.ext_delivery_id.
func buildDeliveryAcceptedEvent(tenantID string, body *deliveryBody) (*domain.Event, error) {
	return domain.NewEvent(tenantID,
		domain.AggregateRef{Type: string(domain.AggDelivery), ID: domain.NewID(domain.PrefixDelivery)},
		"delivery.accepted", "", "", domain.EventActor{Kind: string(domain.ActorGitHub), ID: "github"},
		map[string]any{
			"source":          body.Source,
			"ext_delivery_id": body.ExtDeliveryID,
			"normalized_kind": body.EventKind,
			"repo":            body.Repo,
		})
}

// deliveryBody matches internal-protocols §1.
type deliveryBody struct {
	Source        string          `json:"source"`
	ExtDeliveryID string          `json:"ext_delivery_id"`
	EventKind     string          `json:"event_kind"`
	Repo          string          `json:"repo"`
	ReceivedAt    string          `json:"received_at"`
	Payload       json.RawMessage `json:"payload"`
}

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
		writeRawJSON(w, cached.ResponseCode, []byte(`{"accepted":true,"replay":true}`))
		s.metrics.Inc("sauron_ctrl_http_requests_total", "200")
		return
	}

	ev, err := buildDeliveryAcceptedEvent(s.cfg.TenantID, &body)
	if err != nil {
		WriteDomainError(w, err)
		return
	}
	err = s.store.ExecTx(r.Context(), func(tx pgx.Tx) error {
		if err := s.store.AppendEventsTx(r.Context(), tx, []*domain.Event{ev}); err != nil {
			return err
		}
		s.metrics.Add("sauron_ctrl_events_appended_total", 1)
		return store.RecordCommandTx(r.Context(), tx, s.cfg.TenantID,
			"POST /internal/ctrl/deliveries", extID, requestHash(raw), http.StatusAccepted,
			[]byte(`{"accepted":true}`))
	})
	if err != nil {
		retry := 5
		WriteError(w, http.StatusServiceUnavailable, "unavailable", "storage unavailable; redeliver",
			nil, &retry, nil)
		w.Header().Set("Retry-After", "5")
		return
	}
	writeRawJSON(w, http.StatusAccepted, []byte(`{"accepted":true}`))
	s.metrics.Inc("sauron_ctrl_http_requests_total", "202")
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
		WriteError(w, http.StatusUnauthorized, "unauthorized", "bad signature", nil, nil, nil)
		s.metrics.Inc("sauron_ctrl_http_requests_total", "401")
		return
	}
	body := deliveryBody{
		Source:        "github",
		ExtDeliveryID: r.Header.Get("X-GitHub-Delivery"),
		EventKind:     r.Header.Get("X-GitHub-Event"),
	}
	ev, err := buildDeliveryAcceptedEvent(s.cfg.TenantID, &body)
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
