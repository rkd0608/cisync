package api

import (
	"net/http"
	"time"
)

// handleHealth reports liveness.
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// handleMetrics renders the Prometheus text exposition.
func (s *Server) handleMetrics(w http.ResponseWriter, r *http.Request) {
	s.metrics.Set("cisync_ctrl_process_uptime_seconds", time.Since(s.started).Seconds())
	if s.store != nil {
		if depth, err := s.store.OutboxDepth(r.Context()); err == nil {
			s.metrics.Set("cisync_ctrl_outbox_depth", float64(depth))
		}
	}
	w.Header().Set("Content-Type", "text/plain; version=0.0.4")
	_, _ = w.Write([]byte("# TYPE cisync_ctrl_http_requests_total counter\n"))
	_, _ = w.Write([]byte("# TYPE cisync_ctrl_outbox_depth gauge\n"))
	_, _ = w.Write([]byte("# TYPE cisync_ctrl_process_uptime_seconds gauge\n"))
	_, _ = w.Write([]byte(s.metrics.Render()))
}
