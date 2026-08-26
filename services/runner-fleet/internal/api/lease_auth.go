package api

import (
	"net/http"

	"cisync.dev/cisync/runner-fleet/internal/joblease"
	"cisync.dev/cisync/runner-fleet/internal/store"
)

// authorizeJobMutation enforces the job-lease credential gate (THREAT_MODEL
// B2 / I-04) shared by heartbeat/complete/cancel:
//
//  1. Authorization: Bearer <job-lease-token> MUST be present and carry a
//     valid Ed25519 signature, aud="cisync-fleet" and unexpired exp — any
//     failure here is the opaque typed 401 unauthorized (existence of a run
//     is never revealed to unauthenticated callers).
//  2. Claims must BIND to the request: run_id equals the path value and
//     attempt equals the stored job attempt (the claims the job-lease JWT
//     vouches for under B2). Fence CURRENCY is deliberately not decided
//     here: stale epochs are the store's fenced conditional-write ruling
//     (409 fence_mismatch, I-11), keeping authentication and fencing in
//     separate failure domains so a reclaimed job remains completable by
//     whichever worker genuinely holds the newer epoch under the same
//     dispatch lease.
//
// A nil verifier fails closed: no key configured means no mutation ever
// authenticates (the dev compose always mounts the public key).
func authorizeJobMutation(w http.ResponseWriter, r *http.Request, st store.Store, verifier *joblease.Verifier, presentedFence *int64) (joblease.Claims, bool) {
	if verifier == nil {
		writeError(w, http.StatusUnauthorized, "unauthorized", "job-lease verification is not configured")
		return joblease.Claims{}, false
	}
	rawToken, ok := joblease.FromAuthorizationHeader(r.Header.Get("Authorization"))
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "missing bearer job-lease token")
		return joblease.Claims{}, false
	}
	claims, err := verifier.Verify(rawToken)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "unauthorized", "invalid job-lease token")
		return joblease.Claims{}, false
	}
	runID := r.PathValue("run_id")
	job, err := st.Get(r.Context(), runID)
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found", "unknown run")
		return joblease.Claims{}, false
	}
	if claims.RunID != runID || claims.Attempt != job.Attempt {
		writeError(w, http.StatusUnauthorized, "unauthorized", "lease claims do not bind to this job")
		return joblease.Claims{}, false
	}
	return claims, true
}
