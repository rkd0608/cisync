package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"
)

// ActorKind enumerates event actor classes (events.schema.json $defs/actorKind).
type ActorKind string

// Actor kinds.
const (
	ActorAgent  ActorKind = "agent"
	ActorHuman  ActorKind = "human"
	ActorSystem ActorKind = "service"
	ActorGitHub ActorKind = "github"
)

// AggregateType enumerates ledger aggregate classes.
type AggregateType string

// Aggregate types (Envelope.aggregate.type enum).
const (
	AggIntent      AggregateType = "intent"
	AggLease       AggregateType = "lease"
	AggCandidate   AggregateType = "candidate"
	AggCluster     AggregateType = "cluster"
	AggPlan        AggregateType = "validation_plan"
	AggRun         AggregateType = "validation_run"
	AggEvidence    AggregateType = "evidence"
	AggFailureCase AggregateType = "failure_case"
	AggRepairTask  AggregateType = "repair_task"
	AggPolicy      AggregateType = "policy"
	AggDecision    AggregateType = "decision"
	AggDelivery    AggregateType = "delivery"
)

// Event is the frozen ledger envelope (packages/contracts/events.schema.json).
type Event struct {
	ID            string            `json:"id"`
	Seq           int64             `json:"seq"`
	Type          string            `json:"type"`
	Version       int               `json:"version"`
	TenantID      string            `json:"tenant_id"`
	Aggregate     AggregateRef      `json:"aggregate"`
	CausationID   string            `json:"causation_id"`
	CorrelationID string            `json:"correlation_id"`
	Actor         EventActor        `json:"actor"`
	OccurredAt    time.Time         `json:"occurred_at"`
	PayloadSHA256 string            `json:"payload_sha256"`
	PrevHash      string            `json:"prev_hash"`
	EntryHash     string            `json:"entry_hash"`
	Payload       map[string]any    `json:"payload"`
	Meta          map[string]string `json:"-"`
}

// AggregateRef identifies the aggregate an event belongs to.
type AggregateRef struct {
	Type string `json:"type"`
	ID   string `json:"id"`
}

// EventActor stamps who caused the event.
type EventActor struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
}

// HashPayload returns the canonical payload digest ("sha256:<hex>").
func HashPayload(payload any) (string, error) {
	b, err := CanonicalJSON(payload)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	return HashPrefix + hex.EncodeToString(sum[:]), nil
}

// ComputeEntryHash derives entry_hash =
// sha256(seq | id | type | version | payload_sha256 | prev_hash) where the
// hash inputs are the bare lowercase hex digests and fields are joined with
// "|". The returned value carries the "sha256:" prefix per contract.
func ComputeEntryHash(seq int64, id, eventType string, version int, payloadSHA, prevHash string) string {
	h := sha256.New()
	fmt.Fprintf(h, "%d|%s|%s|%d|%s|%s",
		seq, id, eventType, version,
		trimHashPrefix(payloadSHA), trimHashPrefix(prevHash))
	return HashPrefix + hex.EncodeToString(h.Sum(nil))
}

func trimHashPrefix(s string) string {
	if len(s) > len(HashPrefix) && s[:len(HashPrefix)] == HashPrefix {
		return s[len(HashPrefix):]
	}
	return s
}

// NewEvent builds an envelope with a fresh id and computed payload digest;
// Seq/PrevHash/EntryHash are filled by the store during the chained append.
func NewEvent(tenantID string, agg AggregateRef, eventType string, causationID, correlationID string, actor EventActor, payload map[string]any) (*Event, error) {
	psha, err := HashPayload(payload)
	if err != nil {
		return nil, err
	}
	if correlationID == "" {
		correlationID = NewCorrelationID()
	}
	return &Event{
		ID:            NewID(PrefixEvent),
		Type:          eventType,
		Version:       1,
		TenantID:      tenantID,
		Aggregate:     agg,
		CausationID:   causationID,
		CorrelationID: correlationID,
		Actor:         actor,
		OccurredAt:    time.Now().UTC(),
		PayloadSHA256: psha,
		Payload:       payload,
	}, nil
}
