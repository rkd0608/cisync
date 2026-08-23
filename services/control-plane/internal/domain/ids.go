package domain

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/oklog/ulid/v2"
)

// Prefixes for every ULID-based identifier class in the system.
const (
	PrefixEvent     = "evt_"
	PrefixTenant    = "org_"
	PrefixIntent    = "int_"
	PrefixCandidate = "cand_"
	PrefixCluster   = "clus_"
	PrefixPlan      = "val_"
	PrefixRun       = "run_"
	PrefixEvidence  = "ev_"
	PrefixFailure   = "fc_"
	PrefixRepair    = "repair_"
	PrefixLease     = "lease_"
	PrefixPolicy    = "pol_"
	PrefixDecision  = "dec_"
	PrefixCorr      = "corr_"
	PrefixDelivery  = "dlv_"
)

var ulidMu sync.Mutex

// NewID returns a new prefixed ULID (monotonic within this process).
func NewID(prefix string) string {
	ulidMu.Lock()
	defer ulidMu.Unlock()
	return prefix + ulid.MustNew(ulid.Now(), rand.Reader).String()
}

// NewCorrelationID returns a fresh correlation id for an inbound command.
func NewCorrelationID() string { return NewID(PrefixCorr) }

// HashPrefix prefixes every stored digest (events.schema.json $defs/sha256).
const HashPrefix = "sha256:"

// CanonicalJSON marshals v with encoding/json's deterministic map key order;
// it is the canonical byte form used for payload digests.
func CanonicalJSON(v any) ([]byte, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("canonical json: %w", err)
	}
	return b, nil
}
