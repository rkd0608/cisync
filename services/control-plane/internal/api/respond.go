package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"sauron.dev/sauron/control-plane/internal/domain"
)

// ErrorBody mirrors openapi.yaml ErrorEnvelope.error.
type ErrorBody struct {
	Code        string         `json:"code"`
	Message     string         `json:"message"`
	Details     map[string]any `json:"details,omitempty"`
	RetryAfter  *int           `json:"retry_after_s,omitempty"`
	Suggestions []string       `json:"suggestions,omitempty"`
}

// ErrorEnvelope is the uniform non-2xx response shape.
type ErrorEnvelope struct {
	Error ErrorBody `json:"error"`
}

// WriteJSON serializes v with the given status.
func WriteJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if v != nil {
		enc := json.NewEncoder(w)
		if err := enc.Encode(v); err != nil {
			logError("write json: %v", err)
		}
	}
}

// WriteError renders the uniform error envelope for a code/status pair.
func WriteError(w http.ResponseWriter, status int, code, message string, details map[string]any, retryAfter *int, suggestions []string) {
	WriteJSON(w, status, ErrorEnvelope{Error: ErrorBody{
		Code: code, Message: message, Details: details,
		RetryAfter: retryAfter, Suggestions: suggestions,
	}})
}

// WriteDomainError maps domain sentinels onto the contract's codes.
func WriteDomainError(w http.ResponseWriter, err error) {
	var detail string
	switch {
	case errors.Is(err, domain.ErrNotFound):
		WriteError(w, http.StatusNotFound, "not_found", "resource not found", nil, nil, nil)
	case errors.Is(err, domain.ErrUnauthorized):
		WriteError(w, http.StatusUnauthorized, "unauthorized", "missing or invalid bearer token", nil, nil, nil)
	case errors.Is(err, domain.ErrBadSignature):
		WriteError(w, http.StatusUnauthorized, "unauthorized", "bad signature", nil, nil, nil)
	case errors.Is(err, domain.ErrLateSubmission):
		detail = "late_submission"
		WriteError(w, http.StatusConflict, "conflict_state", "intent closed to new submissions", map[string]any{"reason": detail}, nil, nil)
	case errors.Is(err, domain.ErrDuplicateHead):
		detail = "duplicate_sha"
		WriteError(w, http.StatusConflict, "conflict_state", "identical head_sha already live on this intent", map[string]any{"reason": detail}, nil, nil)
	case errors.Is(err, domain.ErrLeaseNotRenewable):
		detail = "expired_lease"
		WriteError(w, http.StatusConflict, "conflict_state", "lease is not renewable; request a fresh grant", map[string]any{"reason": detail}, nil, nil)
	case errors.Is(err, domain.ErrBudgetExceeded):
		retry := int((15 * time.Minute).Seconds())
		WriteError(w, http.StatusTooManyRequests, "budget_exceeded",
			"tenant compute budget exhausted",
			map[string]any{"kind": "cpu_minutes", "limit": 5000},
			&retry,
			[]string{"reduce expected_surfaces", "defer non-urgent intents", "request quota increase"})
	case errors.Is(err, domain.ErrRateLimited):
		retry := int((30 * time.Second).Seconds())
		WriteError(w, http.StatusTooManyRequests, "rate_limited", "too many requests", nil, &retry, nil)
	case errors.Is(err, domain.ErrValidationFailed):
		WriteError(w, http.StatusBadRequest, "validation_failed", err.Error(), nil, nil, nil)
	default:
		wrapped := fmt.Sprintf("%v", err)
		logError("internal error: %s", wrapped)
		retry := 2
		WriteError(w, http.StatusServiceUnavailable, "unavailable", "control plane busy; retry", nil, &retry, nil)
	}
}

// logError prints operational errors to stderr without leaking across the
// package boundary via panics.
func logError(format string, args ...any) {
	fmt.Fprintf(osStderr, "control-plane: "+format+"\n", args...)
}
