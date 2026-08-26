package api

import (
	"context"
	"crypto/subtle"
	"net/http"
	"strings"

	"cisync.dev/cisync/control-plane/internal/audit"
)

type ctxKey int

const tenantKey ctxKey = 0

// TenantFrom returns the authenticated tenant id for the request.
func TenantFrom(ctx context.Context) string {
	v, _ := ctx.Value(tenantKey).(string)
	return v
}

// requireAuth enforces Bearer CISYNC_CTRL_ADMIN_TOKEN and derives tenant_id
// from the token claim only (invariant I-14).
func (s *Server) requireAuth(next func(http.ResponseWriter, *http.Request)) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		const prefix = "Bearer "
		if !strings.HasPrefix(auth, prefix) {
			s.auditAuthzRejection(r, "missing_bearer_token")
			WriteError(w, http.StatusUnauthorized, "unauthorized", "missing bearer token", nil, nil, nil)
			s.metrics.Inc("cisync_ctrl_http_requests_total", "401")
			return
		}
		token := strings.TrimPrefix(auth, prefix)
		if subtle.ConstantTimeCompare([]byte(token), []byte(s.cfg.AdminToken)) != 1 {
			s.auditAuthzRejection(r, "invalid_bearer_token")
			WriteError(w, http.StatusUnauthorized, "unauthorized", "invalid bearer token", nil, nil, nil)
			s.metrics.Inc("cisync_ctrl_http_requests_total", "401")
			return
		}
		ctx := context.WithValue(r.Context(), tenantKey, s.cfg.TenantID)
		next(w, r.WithContext(ctx))
	}
}

// auditAuthzRejection emits one B7 authz_rejected audit event per rejected
// request. WHY fire-and-forget (streamed, not same-tx): the caller is
// UNAUTHENTICATED, so persistence latency here would let anyone throttle the
// API by spamming bad credentials; the bounded stream absorbs floods with
// drop-oldest shedding.
func (s *Server) auditAuthzRejection(r *http.Request, reason string) {
	ev, err := audit.New(s.cfg.TenantID, audit.KindAuthzRejected,
		audit.Actor{Kind: "anonymous", ID: ""},
		map[string]any{"method": r.Method, "path": r.URL.Path},
		map[string]any{"reason": reason})
	if err != nil {
		return // malformed audit payloads must never alter the 401 path
	}
	s.audit.Emit(ev)
}
