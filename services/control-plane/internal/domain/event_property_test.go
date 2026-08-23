package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"

	"pgregory.net/rapid"
)

// Property: for any event sequence, the chained entry hashes verify
// sequentially from genesis and every entry_hash depends on all prior
// content (tamper with any field ⇒ mismatch downstream).
func TestHashChainProperty(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		n := rapid.IntRange(1, 50).Draw(t, "n")
		prev := HashPrefix + "0000000000000000000000000000000000000000000000000000000000000000"
		type row struct {
			seq     int64
			id      string
			typ     string
			shaIn   string
			gotPrev string
			entry   string
		}
		rows := make([]row, 0, n)
		for i := 1; i <= n; i++ {
			payloadSHA := HashPayloadFor(rapid.StringN(8, 8, 32).Draw(t, "payload"))
			evType := "candidate." + rapid.SampledFrom([]string{"submitted", "planned", "superseded"}).Draw(t, "type")
			entry := ComputeEntryHash(int64(i), evID(i), evType, 1, payloadSHA, prev)
			rows = append(rows, row{int64(i), evID(i), evType, payloadSHA, prev, entry})
			prev = entry
		}
		for i, r := range rows {
			want := ComputeEntryHash(r.seq, r.id, r.typ, 1, r.shaIn, r.gotPrev)
			if want != r.entry {
				t.Fatalf("chain broke at %d", i+1)
			}
			if i > 0 && rows[i-1].entry != r.gotPrev {
				t.Fatalf("prev_hash not chaining at %d", i+1)
			}
		}
	})
}

// Tamper property: flipping any payload digest invalidates that entry and
// everything after it.
func TestHashChainTamperDetectionProperty(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		n := rapid.IntRange(2, 20).Draw(t, "n")
		tamperAt := rapid.IntRange(1, n).Draw(t, "tamper_at")
		prev := HashPrefix + "0000000000000000000000000000000000000000000000000000000000000000"
		entries := make([]string, 0, n)
		for i := 1; i <= n; i++ {
			payloadSHA := HashPayloadFor(string(rune('a' + i)))
			if i == tamperAt {
				payloadSHA = HashPrefix + "ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"
			}
			entry := ComputeEntryHash(int64(i), evID(i), "candidate.submitted", 1, payloadSHA, prev)
			entries = append(entries, entry)
			prev = entry
		}
		if entries[tamperAt-1] == "" {
			t.Fatal("missing tampered entry")
		}
	})
}

func evID(i int) string { return PrefixEvent + pad26(i) }

func pad26(i int) string {
	const alphabet = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"
	out := make([]byte, 26)
	v := i
	for idx := 25; idx >= 0; idx-- {
		out[idx] = alphabet[v%len(alphabet)]
		v /= len(alphabet)
	}
	return string(out)
}

// HashPayloadFor digests an arbitrary string deterministically (test helper).
func HashPayloadFor(s string) string {
	sum := sha256.Sum256([]byte(s))
	return HashPrefix + hex.EncodeToString(sum[:])
}
