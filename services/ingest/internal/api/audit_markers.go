package api

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"sauron.dev/sauron/ingest/internal/forward"
)

// markerBuffer bounds queued signature-failure markers awaiting their
// control-plane hop. Overflow sheds OLDEST (counted) — a bad-signature flood
// must not grow memory unboundedly, and each shed is observable.
const markerBuffer = 256

// markerFlushTimeout bounds graceful drain at shutdown.
const markerFlushTimeout = 2 * time.Second

// markerSender forwards quarantine audit markers to control-plane on ONE
// background goroutine. WHY async: markers ride the same HMAC'd ctrl hop as
// deliveries, but emitting them INLINE would let anyone gating signature
// failures throttle ingest's 401 path on control-plane latency; the bounded
// queue absorbs floods with drop-oldest shedding instead.
type markerSender struct {
	forwarder *forward.Forwarder
	ch        chan forward.Envelope
	done      chan struct{}
	mu        sync.Mutex
	dropped   int64
	stopped   bool
}

func newMarkerSender(fw *forward.Forwarder, logger *slog.Logger) *markerSender {
	m := &markerSender{
		forwarder: fw,
		ch:        make(chan forward.Envelope, markerBuffer),
		done:      make(chan struct{}),
	}
	go m.run(logger)
	return m
}

func (m *markerSender) run(logger *slog.Logger) {
	defer close(m.done)
	ctx := context.Background()
	for env := range m.ch {
		if _, err := m.forwarder.Send(ctx, env); err != nil {
			logger.Warn("signature-failure marker forward failed",
				slog.String("ext_delivery_id", env.ExtDeliveryID), slog.String("err", err.Error()))
		}
	}
}

// send queues one marker; drops the OLDEST queued marker when full.
func (m *markerSender) send(env forward.Envelope) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.stopped {
		return
	}
	select {
	case m.ch <- env:
	default:
		select {
		case <-m.ch:
			m.dropped++
		default:
		}
		select {
		case m.ch <- env:
		default:
			m.dropped++
		}
	}
}

// close stops accepting markers and waits briefly for the drain.
func (m *markerSender) close() {
	m.mu.Lock()
	if m.stopped {
		m.mu.Unlock()
		return
	}
	m.stopped = true
	close(m.ch)
	m.mu.Unlock()
	select {
	case <-m.done:
	case <-time.After(markerFlushTimeout):
	}
}
