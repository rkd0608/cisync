package audit

import (
	"context"
	"sync"
	"time"
)

// Sink persists one audit event; returning an error means the event is lost
// (counted as dropped) — the stream never retries, because audit emission
// must never back-pressure the triggering request.
type Sink func(ctx context.Context, ev Event) error

// Hooks let the embedder wire metrics without importing a metrics package.
// Both are optional; they are invoked while the stream lock is NOT held.
type Hooks struct {
	// OnEmitted fires after the sink accepted an event AND for events
	// persisted synchronously by callers via CountDirect (same-tx inserts).
	OnEmitted func(kind Kind)
	// OnDropped fires when an event is discarded: buffer overflow
	// (drop-oldest) or sink error.
	OnDropped func(kind Kind)
}

// DefaultCapacity bounds the in-memory queue. At ~1KB per event this caps
// worst-case memory well under 1MB while absorbing webhook-flood bursts;
// overflow sheds OLDEST events because the newest signal is the one the
// operator is investigating.
const DefaultCapacity = 1024

// Stream is a bounded fire-and-forget audit emitter. WHY fire-and-forget:
// security-audit emission sits on hot paths (auth middleware, completion
// ingestion); blocking them on DB writes would let attackers gate service
// latency on audit volume. The bounded buffer + drop-oldest keeps memory
// flat under flood; every shed is observable via OnDropped.
type Stream struct {
	sink     Sink
	hooks    Hooks
	capacity int

	mu      sync.Mutex
	cond    *sync.Cond
	buf     []Event // FIFO; buf[0] is oldest
	stopped bool
	dropped map[Kind]int64

	done chan struct{} // closed once the worker has fully exited
}

// NewStream starts the background drain worker immediately. Stop MUST be
// called at shutdown to flush buffered events.
func NewStream(capacity int, sink Sink, hooks Hooks) *Stream {
	if capacity <= 0 {
		capacity = DefaultCapacity
	}
	s := &Stream{
		sink:     sink,
		hooks:    hooks,
		capacity: capacity,
		dropped:  map[Kind]int64{},
		done:     make(chan struct{}),
	}
	s.cond = sync.NewCond(&s.mu)
	go s.worker()
	return s
}

// ReplaceSink swaps the persistence sink. WHY a mutable sink: production
// binds the store at construction, while httptest emission tests inject a
// capturing sink after NewServer; swapping is safe between Stop cycles and
// guarded by the stream lock.
func (s *Stream) ReplaceSink(sink Sink) {
	s.mu.Lock()
	s.sink = sink
	s.mu.Unlock()
}

// Emit enqueues ev without blocking. When the buffer is full the OLDEST
// queued event is shed (counted per kind) — never the caller's latency.
func (s *Stream) Emit(ev Event) {
	var dropped Kind
	s.mu.Lock()
	if s.stopped {
		s.mu.Unlock()
		return
	}
	if len(s.buf) >= s.capacity {
		shed := s.buf[0]
		s.buf = s.buf[1:]
		s.dropped[shed.Kind]++
		dropped = shed.Kind
	}
	s.buf = append(s.buf, ev)
	s.cond.Signal()
	s.mu.Unlock()
	// Hooks fire outside the stream lock so they may take their own.
	if dropped != "" {
		s.notifyDrop(dropped)
	}
}

// CountDirect records an event persisted synchronously by the caller (e.g.
// inserted inside the triggering effect transaction). It only feeds the
// emitted/dropped accounting so both persistence paths surface identically
// in metrics.
func (s *Stream) CountDirect(ev Event) {
	s.mu.Lock()
	s.notifyEmit(ev.Kind)
	s.mu.Unlock()
}

// Stop stops accepting events, drains whatever is still buffered, and waits
// up to timeout for the worker to exit. It reports whether the flush
// completed; leftover events are counted dropped.
func (s *Stream) Stop(timeout time.Duration) bool {
	s.mu.Lock()
	s.stopped = true
	s.cond.Broadcast()
	s.mu.Unlock()

	select {
	case <-s.done:
		return true
	case <-time.After(timeout):
		return false
	}
}

// DroppedSnapshot returns cumulative per-kind drop counts (tests/ops).
func (s *Stream) DroppedSnapshot() map[Kind]int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[Kind]int64, len(s.dropped))
	for k, v := range s.dropped {
		out[k] = v
	}
	return out
}

func (s *Stream) worker() {
	defer close(s.done)
	ctx := context.Background()
	for {
		s.mu.Lock()
		for len(s.buf) == 0 && !s.stopped {
			s.cond.Wait()
		}
		if len(s.buf) == 0 && s.stopped {
			s.mu.Unlock()
			return
		}
		ev := s.buf[0]
		s.buf = s.buf[1:]
		s.mu.Unlock()

		if err := s.sink(ctx, ev); err != nil {
			s.mu.Lock()
			s.dropped[ev.Kind]++
			s.mu.Unlock()
			s.notifyDrop(ev.Kind)
			continue
		}
		s.mu.Lock()
		s.notifyEmit(ev.Kind)
		s.mu.Unlock()
	}
}

// notify helpers run WITHOUT the stream lock held so sinks/hooks can take
// their own locks freely.
func (s *Stream) notifyEmit(kind Kind) {
	if s.hooks.OnEmitted != nil {
		s.hooks.OnEmitted(kind)
	}
}

func (s *Stream) notifyDrop(kind Kind) {
	if s.hooks.OnDropped != nil {
		s.hooks.OnDropped(kind)
	}
}
