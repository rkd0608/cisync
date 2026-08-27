package api

import (
	"context"
	"net/http"
	"strings"
	"time"
)

// Session-token middleware for the public /v1/auth surface (SPEC §3
// 2026-08-26). Split from auth_handler.go so each file stays under the 250
// line charter cap (§1).

// sessionEmailKey carries the verified session identity into handlers; same
// pattern as the admin-bearer tenantKey in auth.go.
const sessionEmailKey ctxKey = 1

// bearerToken extracts the raw token from "Authorization: Bearer <token>"
// with the same strictness as the joblease header parser (no interior
// whitespace, non-empty).
func bearerToken(r *http.Request) (string, bool) {
	auth := r.Header.Get("Authorization")
	const prefix = "Bearer "
	if len(auth) <= len(prefix) || !strings.EqualFold(auth[:len(prefix)], prefix) {
		return "", false
	}
	token := strings.TrimSpace(auth[len(prefix):])
	if token == "" || strings.ContainsAny(token, " \t") {
		return "", false
	}
	return token, true
}

// requireSession guards GET /v1/auth/me with the session JWT (NOT the admin
// bearer — /v1/auth/* is public surface; me needs a valid session instead).
func (s *Server) requireSession(next func(http.ResponseWriter, *http.Request)) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.sessionVerifier == nil {
			writeAuthError(w, http.StatusUnauthorized, "unauthorized", "missing or invalid session")
			return
		}
		raw, ok := bearerToken(r)
		if ok {
			if claims, err := s.sessionVerifier.Verify(raw, time.Now()); err == nil {
				ctx := context.WithValue(r.Context(), sessionEmailKey, claims.Email)
				next(w, r.WithContext(ctx))
				return
			}
		}
		s.auditAuthzRejection(r, "invalid_session_token")
		s.metrics.Inc("cisync_ctrl_http_requests_total", "401")
		writeAuthError(w, http.StatusUnauthorized, "unauthorized", "missing or invalid session")
	}
}

// handleAuthMe GET /v1/auth/me → 200 {user{email}} from verified claims.
func (s *Server) handleAuthMe(w http.ResponseWriter, r *http.Request) {
	email, _ := r.Context().Value(sessionEmailKey).(string)
	WriteJSON(w, http.StatusOK, map[string]any{"user": map[string]string{"email": email}})
}
