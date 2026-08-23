package server

import (
	"context"
	"time"

	"sauron.dev/sauron/ingest/internal/domain"
)

// GaugeDeliveries refreshes the deliveries-by-status gauges on every tick
// until ctx is cancelled.
func (s *Server) GaugeDeliveries(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.gaugeOnce(ctx)
		}
	}
}

func (s *Server) gaugeOnce(ctx context.Context) {
	counts, err := s.store.CountByStatus(ctx)
	if err != nil {
		return
	}
	pending := counts[domain.StatusPending] + counts[domain.StatusForwardFailed]
	s.Metrics.GaugeSet("ingest_deliveries_pending", "Deliveries awaiting forward", float64(pending), "status", "unforwarded")
}
