package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5"

	"cisync.dev/cisync/control-plane/internal/audit"
	"cisync.dev/cisync/control-plane/internal/store"
)

// delivery_audit_seams.go hosts the B7/H2 record-only seams of the delivery
// endpoint: signature-failure quarantine markers and duplicate-suspect
// diagnostics. Split from delivery_handler.go to honor the charter's 250-line
// file cap; both paths share one property — they NEVER mutate domain state.

// deliveryBody matches internal-protocols §1. QuarantineReason and
// DuplicateSuspect are ADDITIVE internal-protocol extensions carried by
// ingest (proposed §3 change-log row): they mark deliveries that must be
// audited/logged WITHOUT domain effects.
type deliveryBody struct {
	Source        string          `json:"source"`
	ExtDeliveryID string          `json:"ext_delivery_id"`
	EventKind     string          `json:"event_kind"`
	Repo          string          `json:"repo"`
	ReceivedAt    string          `json:"received_at"`
	Payload       json.RawMessage `json:"payload"`
	// QuarantineReason is non-empty only on signature-failure markers
	// minted by ingest when it quarantines a bad-signature webhook.
	QuarantineReason string `json:"quarantine_reason,omitempty"`
	// DuplicateSuspect marks fresh-GUID content that ingest's replay
	// seen-window flagged as a probable near-replay inside 24h (H2).
	DuplicateSuspect bool `json:"duplicate_suspect,omitempty"`
}

// SEAM DECISION (B7 webhook signature failures): ingest forwards a SIGNED
// quarantine marker through this very endpoint instead of control-plane
// scraping ingest rows — ctrl owns only schema ctrl (ARCHITECTURE §2), so a
// pull seam would cross service ownership, while the existing HMAC'd hop
// already provides transport trust and I-12 exactly-once semantics. Marker
// ext ids are nonce-suffixed per triggering request so every rejected
// attempt audits exactly once without colliding with a later VALID
// redelivery of the same GitHub GUID.
func (s *Server) applyQuarantineMarker(ctx context.Context, rawHash string, body *deliveryBody) (int, []byte, error) {
	ev, err := audit.New(s.cfg.TenantID, audit.KindWebhookSignatureFailed,
		audit.Actor{Kind: "github", ID: "webhook"},
		map[string]any{"ext_delivery_id": body.ExtDeliveryID, "repo": body.Repo},
		map[string]any{
			"quarantined_ext_delivery_id": quarantinedGUID(body.ExtDeliveryID),
			"event_kind":                  body.EventKind,
			"reason":                      body.QuarantineReason,
		})
	if err != nil {
		return 0, nil, err
	}
	fresh := true
	err = s.store.ExecTx(ctx, func(tx pgx.Tx) error {
		// I-12 guard: marker replays collapse here before any effect.
		var err error
		fresh, err = store.MarkProcessedTx(ctx, tx, "delivery-normalizer", body.ExtDeliveryID)
		if err != nil || !fresh {
			return err
		}
		if err := s.store.InsertSecurityAuditTx(ctx, tx, ev); err != nil {
			return err
		}
		return store.RecordCommandTx(ctx, tx, s.cfg.TenantID,
			"POST /internal/ctrl/deliveries", body.ExtDeliveryID, rawHash, http.StatusAccepted,
			[]byte(`{"accepted":true}`))
	})
	if err != nil {
		return 0, nil, err
	}
	if !fresh {
		// internal-protocols §1 replay semantics.
		return http.StatusOK, []byte(`{"accepted":true,"replay":true}`), nil
	}
	s.metrics.Add("cisync_security_audit_total", 1, "kind", string(audit.KindWebhookSignatureFailed))
	return http.StatusAccepted, []byte(`{"accepted":true}`), nil
}

// quarantinedGUID strips the nonce suffix from a sig-failed marker id
// ("<guid>.sigfailed.<ulid>") to recover the offending GitHub GUID.
func quarantinedGUID(markerID string) string {
	if idx := strings.LastIndex(markerID, ".sigfailed."); idx > 0 {
		return markerID[:idx]
	}
	return markerID
}

// applyDuplicateSuspect records the H2 near-replay diagnostic: 202 accepted,
// processed-guard + command-log committed (I-12), structured log + metric —
// and NOTHING else. WHY no ledger anchor: a duplicate_suspect delivery is by
// definition probably-already-processed content; anchoring it would pollute
// the delivery.accepted record with a second row for the same webhook.
func (s *Server) applyDuplicateSuspect(ctx context.Context, body *deliveryBody, rawHash string) (int, []byte, error) {
	err := s.store.ExecTx(ctx, func(tx pgx.Tx) error {
		fresh, err := store.MarkProcessedTx(ctx, tx, "delivery-normalizer", body.ExtDeliveryID)
		if err != nil {
			return err
		}
		if !fresh {
			return nil
		}
		s.metrics.Add("cisync_ctrl_duplicate_suspect_total", 1)
		logError("duplicate_suspect delivery %s kind=%s repo=%s flagged by ingest seen-window (record-only)",
			body.ExtDeliveryID, body.EventKind, body.Repo)
		return store.RecordCommandTx(ctx, tx, s.cfg.TenantID,
			"POST /internal/ctrl/deliveries", body.ExtDeliveryID, rawHash, http.StatusAccepted,
			[]byte(`{"accepted":true}`))
	})
	if err != nil {
		return 0, nil, err
	}
	return http.StatusAccepted, []byte(`{"accepted":true}`), nil
}

// auditWebhookSignatureFailure streams one B7 event for a signature failure
// detected on the ctrl-facing webhook routes. Fire-and-forget: the caller is
// unauthenticated, so persistence must never gate the 401.
func (s *Server) auditWebhookSignatureFailure(extDeliveryID, eventKind string) {
	ev, err := audit.New(s.cfg.TenantID, audit.KindWebhookSignatureFailed,
		audit.Actor{Kind: "github", ID: "webhook"},
		map[string]any{"ext_delivery_id": extDeliveryID},
		map[string]any{"event_kind": eventKind, "reason": "signature_verification_failed"})
	if err != nil {
		return
	}
	s.audit.Emit(ev)
}
