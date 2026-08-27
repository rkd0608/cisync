package api

import (
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"strings"
	"time"

	"cisync.dev/cisync/control-plane/internal/audit"
	"cisync.dev/cisync/control-plane/internal/authusers"
	"cisync.dev/cisync/control-plane/internal/store"
)

// Login rate-limit posture (SPEC: 5 attempts/min per IP+email via the shared
// ctrl.rate_limits token bucket). WHY per IP+email (not just IP): a single
// attacker should not be able to lock out a whole office NAT; why not just
// email: distributed sprays must still cost the source address.
const (
	loginRateCapacity = 5
	loginRatePerMin   = 5
)

// credentialsRequest is the signup/login body shape. Validation is manual
// (zod-equivalent) below — no schema library in the Go tier.
type credentialsRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

// dummyHash defers to VerifyPassword on unknown emails so that "no such
// user" costs one argon2id run exactly like a wrong password — otherwise the
// timing gap IS an enumeration oracle despite the uniform 401 body.
var dummyHash = mustDummyHash()

func mustDummyHash() string {
	hash, err := authusers.HashPassword(strings.Repeat("timing-equalizer-", 2))
	if err != nil {
		panic("api: dummy password hash generation failed: " + err.Error())
	}
	return hash
}

// emailShape is a deliberately conservative RFC-lite check: one local part,
// exactly one @, domain with at least one dot and no whitespace. WHY manual:
// full RFC parsing rejects useful real-world addresses; we only need to stop
// obvious garbage before it reaches citext.
func validEmail(email string) bool {
	if strings.Count(email, "@") != 1 {
		return false
	}
	at := strings.IndexByte(email, '@')
	if at <= 0 || at == len(email)-1 {
		return false
	}
	local, domain := email[:at], email[at+1:]
	if len(local) > 64 || strings.ContainsAny(email, " \t\r\n") {
		return false
	}
	dot := strings.LastIndexByte(domain, '.')
	return dot > 0 && dot < len(domain)-1 && !strings.Contains(domain, "..")
}

// clientIP prefers the left-most X-Forwarded-For hop (caddy/ALB sit in front
// of ctrl) and falls back to the transport peer.
func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		if hop := strings.TrimSpace(strings.Split(xff, ",")[0]); hop != "" {
			return hop
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func writeAuthError(w http.ResponseWriter, status int, code, message string) {
	WriteError(w, status, code, message, nil, nil, nil)
}

func decodeCredentials(r *http.Request) (credentialsRequest, bool) {
	var creds credentialsRequest
	dec := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 1<<20))
	if dec.Decode(&creds) != nil {
		return creds, false
	}
	return creds, true
}

// handleAuthSignup POST /v1/auth/signup → 201 {user{email}} | 409 exists |
// 400 weak_password|invalid_email.
func (s *Server) handleAuthSignup(w http.ResponseWriter, r *http.Request) {
	if s.store == nil || s.sessionSigner == nil {
		writeAuthError(w, http.StatusServiceUnavailable, "unavailable", "auth storage or session key not configured")
		return
	}
	creds, ok := decodeCredentials(r)
	if !ok {
		writeAuthError(w, http.StatusBadRequest, "validation_failed", "body must be { email, password }")
		return
	}
	email := strings.ToLower(strings.TrimSpace(creds.Email))
	if !validEmail(email) {
		writeAuthError(w, http.StatusBadRequest, "invalid_email", "email address format is invalid")
		return
	}
	hash, err := authusers.HashPassword(creds.Password)
	if err != nil { // WeakPasswordError is the only possible error class here
		writeAuthError(w, http.StatusBadRequest, "weak_password", err.Error())
		return
	}
	user, err := s.store.CreateUser(r.Context(), email, hash)
	if err != nil {
		if errors.Is(err, store.ErrEmailTaken) {
			writeAuthError(w, http.StatusConflict, "exists", "an account with this email already exists")
			return
		}
		logError("signup persist: %v", err)
		writeAuthError(w, http.StatusServiceUnavailable, "unavailable", "control plane busy; retry")
		return
	}
	s.auditAuthEvent(r, "signup", email)
	WriteJSON(w, http.StatusCreated, map[string]any{"user": map[string]string{"email": user.Email}})
}

// handleAuthLogin POST /v1/auth/login → 200 {token,user} | 401 invalid_credentials |
// 429 rate_limited. Success and failure bodies are shape-stable so clients
// cannot probe which emails exist.
func (s *Server) handleAuthLogin(w http.ResponseWriter, r *http.Request) {
	if s.store == nil || s.sessionSigner == nil || s.sessionVerifier == nil {
		writeAuthError(w, http.StatusServiceUnavailable, "unavailable", "auth storage or session key not configured")
		return
	}
	creds, ok := decodeCredentials(r)
	email := strings.ToLower(strings.TrimSpace(creds.Email))
	if !ok || email == "" || creds.Password == "" {
		writeAuthError(w, http.StatusBadRequest, "validation_failed", "body must be { email, password }")
		return
	}
	granted, retryAfter, err := s.store.TakeToken(r.Context(), s.cfg.TenantID,
		"authlogin:"+clientIP(r)+"|"+email, loginRateCapacity, loginRatePerMin)
	if err != nil {
		logError("login rate bucket: %v", err)
		writeAuthError(w, http.StatusServiceUnavailable, "unavailable", "control plane busy; retry")
		return
	}
	if !granted {
		retrySec := int((retryAfter + time.Second - time.Millisecond).Seconds())
		WriteError(w, http.StatusTooManyRequests, "rate_limited",
			"too many login attempts; retry later", nil, &retrySec, nil)
		return
	}
	user, findErr := s.store.FindUserByEmail(r.Context(), email)
	stored := dummyHash
	if findErr == nil {
		stored = user.PasswordHash
	}
	if findErr != nil && !errors.Is(findErr, store.ErrUserNotFound) {
		logError("login lookup: %v", findErr)
		writeAuthError(w, http.StatusServiceUnavailable, "unavailable", "control plane busy; retry")
		return
	}
	if !authusers.VerifyPassword(creds.Password, stored) {
		// Uniform rejection for wrong-password AND unknown-email (dummy-hash
		// above equalizes cost): exactly one message ever leaves this branch.
		s.auditAuthzRejection(r, "invalid_credentials")
		writeAuthError(w, http.StatusUnauthorized, "invalid_credentials", "invalid email or password")
		return
	}
	token, mintErr := s.sessionSigner.Mint(authusers.SessionClaims{Email: email, UID: user.ID}, time.Now())
	if mintErr != nil {
		logError("session mint: %v", mintErr)
		writeAuthError(w, http.StatusServiceUnavailable, "unavailable", "control plane busy; retry")
		return
	}
	if touchErr := s.store.TouchLogin(r.Context(), user.ID, time.Now().UTC()); touchErr != nil {
		// WHY non-fatal: identity + token are already decided; only analytics
		// lose data if last_login_at cannot update.
		logError("touch login: %v", touchErr)
	}
	s.auditAuthEvent(r, "login", email)
	WriteJSON(w, http.StatusOK, map[string]any{
		"token": token,
		"user":  map[string]string{"email": email},
	})
}

// auditAuthEvent emits one B7 security-audit row per successful account
// transition. Fire-and-forget like rejections: latency here would slow the
// hottest unauthenticated endpoints under flood.
func (s *Server) auditAuthEvent(r *http.Request, action, email string) {
	ev, err := audit.New(s.cfg.TenantID, audit.KindAuthnAccepted,
		audit.Actor{Kind: "user", ID: email},
		map[string]any{"method": r.Method, "path": r.URL.Path},
		map[string]any{"action": action})
	if err != nil {
		return // malformed audit payloads must never alter the response path
	}
	s.audit.Emit(ev)
}
