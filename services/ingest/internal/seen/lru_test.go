package seen

import (
	"crypto/sha256"
	"encoding/hex"
	"sync"
	"testing"
	"time"
)

func TestSeenOrAddFlagsSecondIdenticalContent(t *testing.T) {
	start := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)
	current := start
	c := New(4, DefaultTTL, func() time.Time { return current })

	if c.SeenOrAdd("k1") {
		t.Fatal("first sighting must not be a duplicate")
	}
	if !c.SeenOrAdd("k1") {
		t.Fatal("second sighting inside TTL must be flagged duplicate")
	}
	// Advance past the TTL: the same key is fresh again.
	current = start.Add(DefaultTTL + time.Minute)
	if c.SeenOrAdd("k1") {
		t.Fatal("sighting after TTL expiry must not be flagged")
	}
}

func TestLRUEvictsOldestBeyondCapacity(t *testing.T) {
	current := time.Now()
	c := New(3, DefaultTTL, func() time.Time { return current })

	for _, k := range []string{"a", "b", "c", "d"} {
		c.SeenOrAdd(k)
	}
	if c.Len() != 3 {
		t.Fatalf("len = %d, want 3 (capacity bound)", c.Len())
	}
	if c.Evictions() != 1 {
		t.Fatalf("evictions = %d, want 1", c.Evictions())
	}
	// "a" was the LRU entry; re-sighting must NOT be flagged (it was evicted).
	if c.SeenOrAdd("a") {
		t.Fatal("evicted key must be treated as unseen")
	}
	// "d" is still resident: flagged.
	if !c.SeenOrAdd("d") {
		t.Fatal("resident key must stay flagged")
	}
}

func TestAccessRefreshesRecency(t *testing.T) {
	current := time.Now()
	c := New(2, DefaultTTL, func() time.Time { return current })

	c.SeenOrAdd("a")
	c.SeenOrAdd("b")
	// Touch "a" so "b" becomes the LRU tail.
	c.SeenOrAdd("a")
	c.SeenOrAdd("c")

	if c.Len() != 2 {
		t.Fatalf("len = %d, want 2", c.Len())
	}
	if !c.SeenOrAdd("a") {
		t.Fatal("recently-touched key must survive eviction and stay flagged")
	}
	if c.SeenOrAdd("b") {
		t.Fatal("untouched LRU tail must have been evicted, not 'a'")
	}
}

func TestExpiredEntriesStopFlaggingAndCapacityHolds(t *testing.T) {
	start := time.Now()
	current := start
	c := New(2, time.Hour, func() time.Time { return current })

	c.SeenOrAdd("x")
	c.SeenOrAdd("y")

	// Advance past the TTL: everything expires; re-sighting is unflagged
	// (the class is no longer a replay suspect).
	current = start.Add(2 * time.Hour)
	if c.SeenOrAdd("x") {
		t.Fatal("expired entry must not flag as duplicate")
	}
	// Capacity still holds after the re-sighting + new insert.
	c.SeenOrAdd("z")
	c.SeenOrAdd("w")
	if c.Len() != 2 {
		t.Fatalf("len = %d, want 2", c.Len())
	}
}

func TestConcurrentSeenOrAddIsRaceFreeAndCountsOnce(t *testing.T) {
	c := New(1000, DefaultTTL, time.Now)
	var wg sync.WaitGroup
	var duplicates int64
	var mu sync.Mutex
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if c.SeenOrAdd("same-key") {
				mu.Lock()
				duplicates++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if duplicates != 49 {
		t.Fatalf("duplicates = %d, want exactly 49 (first sighting wins)", duplicates)
	}
}

func TestClassHashIsStableUnderKeyOrderAndWhitespace(t *testing.T) {
	a := ClassHash("github", "acme/payments", "push",
		[]byte(`{"ref":"refs/heads/main","before":"aa","after":"bb"}`))
	b := ClassHash("github", "acme/payments", "push",
		[]byte(`{ "after" : "bb" , "before":"aa",   "ref":"refs/heads/main" }`))
	if a != b {
		t.Fatal("key order / whitespace must not change the class hash")
	}
	if len(a) != 64 {
		t.Fatalf("hash length = %d, want 64 hex chars", len(a))
	}
}

func TestClassHashStripsVolatileFieldsAtAnyDepth(t *testing.T) {
	base := `{"repository":{"full_name":"acme/payments"},"commits":[{"id":"1"}]}`
	withVolatile := `{"request_id":"r-9","delivery":"d-2","received_at":"2026-01-01T00:00:00Z",` +
		`"commits":[{"id":"1","uploaded_at":"later"}],"repository":{"full_name":"acme/payments"}}`
	a := ClassHash("github", "acme/payments", "push", []byte(base))
	b := ClassHash("github", "acme/payments", "push", []byte(withVolatile))
	if a != b {
		t.Fatal("volatile-only difference must normalize to the SAME class")
	}
}

func TestClassHashSeparatesIdentityDimensions(t *testing.T) {
	payload := []byte(`{"n":1}`)
	base := ClassHash("github", "acme/payments", "push", payload)
	if ClassHash("github", "acme/other", "push", payload) == base {
		t.Fatal("different repo must hash differently")
	}
	if ClassHash("github", "acme/payments", "pull_request", payload) == base {
		t.Fatal("different event kind must hash differently")
	}
	if ClassHash("github", "acme/payments", "push", []byte(`{"n":2}`)) == base {
		t.Fatal("different content must hash differently")
	}
}

func TestClassHashHandlesUndecodablePayload(t *testing.T) {
	raw := `{not json`
	sum := sha256.Sum256([]byte("github|acme/x|push|" + raw))
	want := hex.EncodeToString(sum[:])
	if got := ClassHash("github", "acme/x", "push", []byte(raw)); got != want {
		t.Fatalf("undecodable payload must fall back to raw bytes: %s != %s", got, want)
	}
}
