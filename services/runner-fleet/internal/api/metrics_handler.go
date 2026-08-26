package api

import (
	"net/http"

	"cisync.dev/cisync/runner-fleet/internal/obs"
)

// MetricsHandler serves GET /metrics in Prometheus text exposition format.
type MetricsHandler struct {
	metrics *obs.Metrics
}

// NewMetricsHandler builds the metrics handler.
func NewMetricsHandler(m *obs.Metrics) *MetricsHandler {
	return &MetricsHandler{metrics: m}
}

// ServeHTTP implements GET /metrics.
func (h *MetricsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(h.metrics.Render()))
}
