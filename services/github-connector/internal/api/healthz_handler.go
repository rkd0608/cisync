package api

import (
	"net/http"
)

// HealthzHandler serves GET /healthz as a pure liveness probe.
type HealthzHandler struct{}

// NewHealthzHandler builds the liveness handler.
func NewHealthzHandler() *HealthzHandler {
	return &HealthzHandler{}
}

// ServeHTTP implements GET /healthz.
func (h *HealthzHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
