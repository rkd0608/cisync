// Package statusapi serves GET /v1/installations/status from ghconn tables:
// the operator-visible answer to "is the webhook loop alive per repo?"
// (plan §5.6 / wave-5 deliverable). Read-only; bearer-auth'd like ctrl.
package statusapi

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"time"

	"cisync.dev/cisync/github-connector/internal/store"
)

// stalledAfter is the receiving→stalled threshold for webhook_state.
const stalledAfter = 15 * time.Minute

// StatusSource is the read-only store surface this handler needs.
type StatusSource interface {
	InstallationStatuses(ctx context.Context, stalledAfter time.Duration, now time.Time) ([]store.InstallationStatus, error)
}

// Handler terminates GET /v1/installations/status.
type Handler struct {
	source StatusSource
	token  string
	now    func() time.Time
}

// NewHandler wires the status endpoint. An empty token fails closed: every
// request is 401 regardless of presented credentials.
func NewHandler(source StatusSource, adminToken string) *Handler {
	return &Handler{source: source, token: adminToken, now: time.Now}
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !h.authorized(r) {
		writeError(w, http.StatusUnauthorized, "unauthorized", "missing or invalid bearer token")
		return
	}
	statuses, err := h.source.InstallationStatuses(r.Context(), stalledAfter, h.now())
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "unavailable", "status projection unavailable")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]any{"installations": statuses})
}

// authorized enforces constant-time Bearer comparison (same posture as
// control-plane's requireAuth).
func (h *Handler) authorized(r *http.Request) bool {
	const prefix = "Bearer "
	auth := r.Header.Get("Authorization")
	if len(h.token) == 0 || !startsWith(auth, prefix) {
		return false
	}
	presented := auth[len(prefix):]
	return subtle.ConstantTimeCompare([]byte(presented), []byte(h.token)) == 1
}

func startsWith(s, prefix string) bool {
	return len(s) >= len(prefix) && s[:len(prefix)] == prefix
}

type errorEnvelope struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	var env errorEnvelope
	env.Error.Code = code
	env.Error.Message = message
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(env)
}
