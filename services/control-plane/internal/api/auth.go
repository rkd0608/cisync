package api

import (
	"context"
	"crypto/subtle"
	"net/http"
	"strings"
)

type ctxKey int

const tenantKey ctxKey = 0

// TenantFrom returns the authenticated tenant id for the request.
func TenantFrom(ctx context.Context) string {
	v, _ := ctx.Value(tenantKey).(string)
	return v
}

// requireAuth enforces Bearer SAURON_CTRL_ADMIN_TOKEN and derives tenant_id
// from the token claim only (invariant I-14).
func (s *Server) requireAuth(next func(http.ResponseWriter, *http.Request)) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		const prefix = "Bearer "
		if !strings.HasPrefix(auth, prefix) {
			WriteError(w, http.StatusUnauthorized, "unauthorized", "missing bearer token", nil, nil, nil)
			s.metrics.Inc("sauron_ctrl_http_requests_total", "401")
			return
		}
		token := strings.TrimPrefix(auth, prefix)
		if subtle.ConstantTimeCompare([]byte(token), []byte(s.cfg.AdminToken)) != 1 {
			WriteError(w, http.StatusUnauthorized, "unauthorized", "invalid bearer token", nil, nil, nil)
			s.metrics.Inc("sauron_ctrl_http_requests_total", "401")
			return
		}
		ctx := context.WithValue(r.Context(), tenantKey, s.cfg.TenantID)
		next(w, r.WithContext(ctx))
	}
}
