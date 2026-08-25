package api

import (
	"encoding/json"
	"strings"
	"testing"

	"sauron.dev/sauron/control-plane/internal/config"
	"sauron.dev/sauron/control-plane/internal/domain"
)

// TestDeliveryAggregateMintsPrefixedUlid pins the events.schema.json
// prefixedUlid contract: delivery aggregate ids are platform-minted dlv_
// ULIDs; the external GitHub GUID appears only in payload.ext_delivery_id.
func TestDeliveryAggregateMintsPrefixedUlid(t *testing.T) {
	body := &deliveryBody{
		Source:        "github",
		ExtDeliveryID: "3f2a1b6c-external-guid",
		EventKind:     "push",
		Repo:          "acme/payments",
	}
	ev, err := buildDeliveryAcceptedEvent(config.DevTenant, body, body.EventKind)
	if err != nil {
		t.Fatalf("build event: %v", err)
	}
	if !strings.HasPrefix(ev.Aggregate.ID, "dlv_") {
		t.Fatalf("aggregate.id must be a dlv_-prefixed ULID, got %q", ev.Aggregate.ID)
	}
	if len(ev.Aggregate.ID) != len("dlv_")+26 {
		t.Fatalf("aggregate.id must be dlv_ + 26-char ULID, got %q", ev.Aggregate.ID)
	}
	if ev.Aggregate.Type != string(domain.AggDelivery) {
		t.Fatalf("aggregate.type = %q want delivery", ev.Aggregate.Type)
	}
	raw, err := json.Marshal(ev.Payload)
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatal(err)
	}
	if payload["ext_delivery_id"] != body.ExtDeliveryID {
		t.Fatalf("external GUID must live in payload.ext_delivery_id, got %v", payload["ext_delivery_id"])
	}
}

// TestDeliveryAggregateIDsUniquePerEvent ensures replays of the same external
// GUID never collide on the minted aggregate identity.
func TestDeliveryAggregateIDsUniquePerEvent(t *testing.T) {
	body := &deliveryBody{Source: "github", ExtDeliveryID: "same-guid", EventKind: "push"}
	first, err := buildDeliveryAcceptedEvent(config.DevTenant, body, body.EventKind)
	if err != nil {
		t.Fatal(err)
	}
	second, err := buildDeliveryAcceptedEvent(config.DevTenant, body, body.EventKind)
	if err != nil {
		t.Fatal(err)
	}
	if first.Aggregate.ID == second.Aggregate.ID {
		t.Fatalf("each accepted delivery must mint its own aggregate id, got %q twice", first.Aggregate.ID)
	}
}

// TestDeliveryAcceptedDigestCoversServedPayload pins I-07 for the §3.1
// label stamping: the payload digest computed at NewEvent time must equal a
// fresh digest over the final served payload map. A post-hash mutation of
// Payload (the original normalized_kind overwrite bug) breaks every live
// chain verification.
func TestDeliveryAcceptedDigestCoversServedPayload(t *testing.T) {
	body := &deliveryBody{
		Source:        "github",
		ExtDeliveryID: "3f2a1b6c-external-guid",
		EventKind:     "pull_request.opened",
		Repo:          "acme/payments",
	}
	ev, err := buildDeliveryAcceptedEvent(config.DevTenant, body, "pr.opened")
	if err != nil {
		t.Fatalf("build event: %v", err)
	}
	if got := ev.Payload["normalized_kind"]; got != "pr.opened" {
		t.Fatalf("normalized_kind = %v, want pr.opened (§3.1 mapping visible in tail)", got)
	}
	digest, err := domain.HashPayload(ev.Payload)
	if err != nil {
		t.Fatalf("recompute digest: %v", err)
	}
	if digest != ev.PayloadSHA256 {
		t.Fatalf("I-07 breach: stored digest does not cover the served payload")
	}
}
