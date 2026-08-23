package domain

import (
	"encoding/json"
	"testing"
)

// CanonicalJSON must produce RECURSIVELY key-sorted output. Struct-typed
// payload values (e.g. ConflictRef) serialize in struct field order, which is
// NOT lexicographic — an independent verifier recomputing payload_sha256 over
// canonical bytes would reject every such event (I-07).
func TestCanonicalJSONSortsStructFieldOrder(t *testing.T) {
	type conflict struct {
		IntentID       string `json:"intent_id"`
		Relation       string `json:"relation"`
		Owner          string `json:"owner"`
		Recommendation string `json:"recommendation"`
	}
	in := map[string]any{
		"conflicts": []any{conflict{IntentID: "int_1", Relation: "overlapping", Owner: "acme/idem", Recommendation: "coordinate"}},
		"budget":    struct {
			CPUMinutes    int64 `json:"cpu_minutes"`
			RepairAttempt int64 `json:"repair_attempts"`
		}{CPUMinutes: 120, RepairAttempt: 2},
	}
	got, err := CanonicalJSON(in)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"budget":{"cpu_minutes":120,"repair_attempts":2},` +
		`"conflicts":[{"intent_id":"int_1","owner":"acme/idem","recommendation":"coordinate","relation":"overlapping"}]}`
	if string(got) != want {
		t.Fatalf("canonical form mismatch:\n got: %s\nwant: %s", got, want)
	}
}

func TestCanonicalJSONPreservesNumberPrecision(t *testing.T) {
	in := map[string]any{"priority": json.Number("0.6666666666666666"), "n": float64(2)}
	got, err := CanonicalJSON(in)
	if err != nil {
		t.Fatal(err)
	}
	// Re-marshal path must not inflate floats or drop integer-ness.
	want := `{"n":2,"priority":0.6666666666666666}`
	if string(got) != want {
		t.Fatalf("got %s, want %s", got, want)
	}
}

func TestCanonicalJSONStableAcrossRebuild(t *testing.T) {
	type inner struct {
		Zed int    `json:"zed"`
		Abi string `json:"abi"`
	}
	in := map[string]any{"k2": []any{inner{Zed: 1, Abi: "x"}}, "k1": map[string]any{"y": 1, "x": nil}}
	first, err := CanonicalJSON(in)
	if err != nil {
		t.Fatal(err)
	}
	var round any
	if err := json.Unmarshal(first, &round); err != nil {
		t.Fatal(err)
	}
	second, err := CanonicalJSON(round)
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(second) {
		t.Fatalf("canonicalization not stable: %s vs %s", first, second)
	}
}
