# Sauron Internal Protocols (FROZEN v1)

Inter-service HTTP contracts not exposed in openapi.yaml. Binding for W1/W2 builders.

## 1. ingest → control-plane (webhook forwarding)

`POST /internal/ctrl/deliveries`

Headers: `Idempotency-Key: <ext_delivery_id>`, `X-Sauron-Signature: sha256=<hex hmac of raw body with shared secret>`, `Content-Type: application/json`.

```json
{
  "source": "github",
  "ext_delivery_id": "<X-GitHub-Delivery>",
  "event_kind": "pull_request.opened|push|...",
  "repo": "acme/payments",
  "received_at": "RFC3339",
  "payload": { "...redacted external payload..." }
}
```

Responses: `202 accepted` · `200 replay (idempotent)` · `401 bad signature` ·
`503 unavailable + Retry-After` (ingest returns same to GitHub so it redelivers).

## 2. control-plane ↔ runner-fleet (execution protocol)

Base: `SAURON_CTRL_FLEET_URL`. Fleet NEVER reads the ledger; control-plane drives it.

- `POST /internal/fleet/jobs/claim`
  `{"pool": "sim", "limit": 4}` →
  `{"jobs": [{"run_id": "run_…", "attempt": 1, "fence_token": 7, "tier": 1, "pool": "sim", "job_spec": {…}}]}`
  Claim is atomic server-side; a run is claimed by ≤1 worker at a time.
- `POST /internal/fleet/jobs/{run_id}/heartbeat` `{"fence_token": 7}` → `204`
- `POST /internal/fleet/jobs/{run_id}/complete`
  `{"fence_token": 7, "status": "succeeded|failed|timed_out", "logs_digest": "sha256:…",
    "artifact_digests": ["sha256:…"], "duration_ms": 42000,
    "actual_cost_millicents": 180}`
  → `200 {"accepted": true}` | `409 {"accepted": false, "reason": "fence_mismatch|already_accepted"}`
  Results uploaded BEFORE this call; stale fence tokens never mutate state (I-11).
- `POST /internal/fleet/jobs/{run_id}/cancel` `{"reason": "superseded"}` → `204` (idempotent)

## 3. Job spec (inside claim payload)

```json
{
  "kind": "hermetic_build|selected_unit|api_compat|contract_suite|sast_diff|…",
  "repo": "acme/payments",
  "base_sha": "…40 hex…",
  "head_sha": "…40 hex…",
  "patch_ref": "bundle-url-or-digest",
  "inputs_hash": "sha256:…",
  "timeout_ms": 900000,
  "sim_profile": {"duration_ms": 800, "outcome_bias": "pass"}
}
```

`sim` provider executes nothing; it deterministically simulates duration/outcome from
`sim_profile` (seeded by run_id hash) — the CI-default provider. `docker` provider runs
real containers (`--network none --read-only`, resource-capped, NOT-FOR-PRODUCTION).
