package audit

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// countingSink records every event the worker delivers.
type countingSink struct {
	mu     sync.Mutex
	events []Event
	fail   atomic.Bool
}

func (c *countingSink) record(_ context.Context, ev Event) error {
	if c.fail.Load() {
		return errors.New("sink unavailable")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.events = append(c.events, ev)
	return nil
}

func (c *countingSink) len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.events)
}

// blockingSink stalls the worker until its gate closes, so tests can fill
// the buffer deterministically and observe drop-oldest. taken is signalled
// each time an event LEAVES the queue (making the in-flight set explicit).
type blockingSink struct {
	mu      sync.Mutex
	once    sync.Once
	gate    chan struct{}
	taken   chan struct{}
	deliver []Event
}

func (b *blockingSink) wait(_ context.Context, ev Event) error {
	// Signal pickup exactly once (the handshake the test waits on); later
	// calls must not block on an unread channel after the gate opened.
	b.once.Do(func() { close(b.taken) })
	<-b.gate
	b.mu.Lock()
	defer b.mu.Unlock()
	b.deliver = append(b.deliver, ev)
	return nil
}

func (b *blockingSink) deliveredCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.deliver)
}

// firstSeq reads detail.seq of the oldest delivered event, proving WHICH
// events survived the shed.
func (b *blockingSink) firstSeq(t *testing.T) int {
	return b.seqAt(t, 0)
}

// secondSeq reads detail.seq of the second-oldest delivered event (the first
// true survivor after any in-flight event).
func (b *blockingSink) secondSeq(t *testing.T) int {
	return b.seqAt(t, 1)
}

func (b *blockingSink) seqAt(t *testing.T, idx int) int {
	t.Helper()
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.deliver) <= idx {
		t.Fatalf("no delivered event at index %d", idx)
	}
	var decoded struct {
		Seq int `json:"seq"`
	}
	if err := json.Unmarshal(b.deliver[idx].Detail, &decoded); err != nil {
		t.Fatalf("decode detail: %v", err)
	}
	return decoded.Seq
}

func mustEvent(t *testing.T, kind Kind, seq int) Event {
	t.Helper()
	ev, err := New("org_test", kind, Actor{Kind: "system", ID: "test"}, nil,
		map[string]any{"seq": seq})
	if err != nil {
		t.Fatalf("build event: %v", err)
	}
	return ev
}

func TestStreamDrainsEveryEmittedEventExactlyOnce(t *testing.T) {
	sink := &countingSink{}
	var emitted int64
	s := NewStream(DefaultCapacity, sink.record, Hooks{
		OnEmitted: func(Kind) { atomic.AddInt64(&emitted, 1) },
	})
	const total = 50
	for i := 0; i < total; i++ {
		s.Emit(mustEvent(t, KindAuthzRejected, i))
	}
	if !s.Stop(5 * time.Second) {
		t.Fatal("stream did not drain before timeout")
	}
	if got := sink.len(); got != total {
		t.Fatalf("drained %d events, want %d", got, total)
	}
	if atomic.LoadInt64(&emitted) != total {
		t.Fatalf("OnEmitted fired %d times, want %d", emitted, total)
	}
	if drops := s.DroppedSnapshot(); len(drops) != 0 {
		t.Fatalf("unexpected drops: %v", drops)
	}
}

func TestStreamDropOldestWhenFull(t *testing.T) {
	const capacity = 8
	gate := make(chan struct{})
	taken := make(chan struct{})
	blocked := &blockingSink{gate: gate, taken: taken}
	var droppedKinds []Kind
	s := NewStream(capacity, blocked.wait, Hooks{
		OnDropped: func(k Kind) { droppedKinds = append(droppedKinds, k) },
	})
	// ev0 leaves the queue and stalls inside the sink (deterministic via
	// the taken handshake): the buffer is empty while it is in flight.
	s.Emit(mustEvent(t, KindBudgetExceeded, 0))
	<-taken
	// Now push 12 more: the first 8 fill the buffer, each further emit
	// sheds the OLDEST queued event (seq 1..4), keeping 5..12.
	for i := 1; i <= 12; i++ {
		s.Emit(mustEvent(t, KindBudgetExceeded, i))
	}
	close(gate)
	if !s.Stop(5 * time.Second) {
		t.Fatal("stream did not drain")
	}
	if got := blocked.deliveredCount(); got != capacity+1 {
		t.Fatalf("delivered %d, want %d (in-flight + survivors)", got, capacity+1)
	}
	if first := blocked.firstSeq(t); first != 0 {
		t.Fatalf("first delivered seq = %d, want 0 (in-flight event)", first)
	}
	if second := blocked.secondSeq(t); second != 5 {
		t.Fatalf("oldest surviving seq = %d, want 5 (drop-oldest violated)", second)
	}
	if drops := s.DroppedSnapshot()[KindBudgetExceeded]; drops != 4 {
		t.Fatalf("dropped counter = %d, want 4", drops)
	}
	if len(droppedKinds) != 4 {
		t.Fatalf("OnDropped fired %d times, want 4", len(droppedKinds))
	}
}

func TestStreamCountsSinkErrorsAsDropped(t *testing.T) {
	sink := &countingSink{}
	sink.fail.Store(true)
	var dropped int64
	s := NewStream(DefaultCapacity, sink.record, Hooks{
		OnDropped: func(Kind) { atomic.AddInt64(&dropped, 1) },
	})
	s.Emit(mustEvent(t, KindFenceMismatch, 1))
	s.Stop(5 * time.Second)
	if atomic.LoadInt64(&dropped) != 1 {
		t.Fatalf("sink failure must count as drop, got %d", dropped)
	}
}

func TestStreamConcurrentEmitIsRaceFree(t *testing.T) {
	sink := &countingSink{}
	s := NewStream(DefaultCapacity, sink.record, Hooks{})
	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				s.Emit(mustEvent(t, KindLeaseRevocation, i))
			}
		}()
	}
	wg.Wait()
	if !s.Stop(10 * time.Second) {
		t.Fatal("stream did not drain")
	}
	if got := sink.len(); got != 800 {
		t.Fatalf("delivered %d, want 800", got)
	}
}

func TestCountDirectFeedsEmittedAccountingOnly(t *testing.T) {
	sink := &countingSink{}
	var emitted int64
	s := NewStream(DefaultCapacity, sink.record, Hooks{
		OnEmitted: func(Kind) { atomic.AddInt64(&emitted, 1) },
	})
	ev := mustEvent(t, KindChainVerifyFailure, 1)
	s.CountDirect(ev)
	s.Stop(time.Second)
	if atomic.LoadInt64(&emitted) != 1 {
		t.Fatalf("direct count = %d, want 1", emitted)
	}
	if got := sink.len(); got != 0 {
		t.Fatalf("direct count must not enqueue, got %d queued rows", got)
	}
}

func TestNewRejectsNonMarshalablePayloads(t *testing.T) {
	bad := map[string]any{"x": make(chan int)} // channels never marshal
	if _, err := New("org_x", KindAuthzRejected, Actor{}, bad, nil); err == nil {
		t.Fatal("unmarshalable subject must error")
	}
}
