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

### Job-lease credentials (THREAT_MODEL B2 / I-04)

Control-plane mints ONE Ed25519-signed job-lease token per dispatched run at
dispatch time (`alg: EdDSA`, compact JWT). Claims:

| claim | value |
|---|---|
| `aud` | `"sauron-fleet"` |
| `jti` | `"fleet:<run_id>:<attempt>:<fence_token>"` |
| `run_id`, `attempt`, `fence_token`, `repo`, `tier` | the dispatch identity |
| `iat`, `exp` | RFC 3339 epoch seconds; TTL ≤ **60 minutes** |

The token rides the enqueue payload (`lease_token`), is stored with the job,
and is handed to the claiming worker in the claim response. Every mutating
call below REQUIRES `Authorization: Bearer <job-lease-token>`. The fleet
verifies signature (public key only), audience, expiry, and that claims bind
to the job (`run_id`, `attempt`; fence currency remains the fenced write's
409 ruling per I-11). Missing/expired/tampered/misbound credentials get

`401 {"error": {"code": "unauthorized", "message": …}}`.

Fleet public key envs: `SAURON_FLEET_JOBLEASYPUB_KEY_FILE` (PEM file) or
`SAURON_FLEET_JOBLEASE_PUB_B64` (base64 inline PEM). Control-plane signs with
its dedicated key from `SAURON_CTRL_JOBLEASE_KEY_FILE`. Unconfigured fleets
fail closed.

### Endpoints

- `POST /internal/fleet/jobs`
  `{"run_id": "run_…", "attempt": 1, "tier": 1, "pool": "sim", "job_spec": {…},
    "lease_token": "<job-lease JWT>"}` → `202 {"accepted": true}`
  Idempotent insert of a claimable execution job.
- `POST /internal/fleet/jobs/claim`
  `{"pool": "sim", "limit": 4}` →
  `{"jobs": [{"run_id": "run_…", "attempt": 1, "fence_token": 7, "tier": 1,
    "pool": "sim", "job_spec": {…}, "lease_token": "<JWT>"}]}`
  Claim is atomic server-side; a run is claimed by ≤1 worker at a time.
- `POST /internal/fleet/jobs/{run_id}/heartbeat`
  Headers: `Authorization: Bearer <job-lease-token>` (required).
  `{"fence_token": 7}` → `204` | `401 unauthorized` | `409 fence_mismatch`
- `POST /internal/fleet/jobs/{run_id}/complete`
  Headers: `Authorization: Bearer <job-lease-token>` (required).
  `{"fence_token": 7, "status": "succeeded|failed|timed_out",
    "logs_digest": "sha256:…",
    "artifact_digests": ["sha256:…"], "duration_ms": 42000,
    "actual_cost_millicents": 180,
    "results": {"total": 8, "passed": 8, "failed": 0, "skipped": 0,
                "quarantined": 0}}`
  → `200 {"accepted": true}` · `401 unauthorized` ·
  `409 {"accepted": false, "reason": "fence_mismatch|already_accepted"}`
  Results are uploaded BEFORE this call; stale fence tokens never mutate
  state (I-11). The `results` census MUST sum to `total` when present;
  providers always populate it so control-plane can validate I-01 against
  REAL executed outcomes (skipped/quarantined are never positive evidence).
- `POST /internal/fleet/jobs/{run_id}/cancel`
  Headers: `Authorization: Bearer <job-lease-token>` (required; binds on
  `run_id`/`attempt` only — no fence is presentable in the cancel body).
  `{"reason": "superseded"}` → `204` (idempotent)

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

## 4. control-plane → github-connector (decision push, W2)

Base: `SAURON_CTRL_CONNECTOR_URL`. The connector is idle-until-fed; control-plane
pushes one envelope per rendered decision via its outbox relay.

- `GET /internal/fleet/jobs/completed?limit=N` (control-plane → fleet, feed)
  `{"jobs": [{"run_id","attempt","fence_token","tier","pool","status",
   "logs_digest","logs_excerpt?","artifact_digests[]","duration_ms",
   "actual_cost_millicents","classification?",
   "results?": {"total","passed","failed","skipped","quarantined"},
   "results_digest?"}]}` — accepted terminal jobs,
   newest first. Consumers dedupe by `(run_id, fence_token)` inside their
   effect tx (I-12), so replays are harmless. The census mirrors the stored
   completion and is the I-01 validation input on the control-plane side.
- `POST /internal/connector/decisions` (control-plane → connector)
  Headers: `Idempotency-Key: <decision_id>`,
  `X-Sauron-Signature: sha256=<hex hmac of raw body with SAURON_CONN_WEBHOOK_SECRET>`,
  `Content-Type: application/json`.

```json
{
  "decision_id": "dec_01J…",
  "candidate_id": "cand_01J…",
  "repo": "acme/payments",
  "head_sha": "…40 hex…",
  "verb": "eligible_for_merge_train|rejected|deferred",
  "confidence": 0.94,
  "policy": {"policy_id": "pol_…", "policy_version": 4},
  "summary": "explanation summary",
  "rendered_at": "RFC3339"
}
```

Responses: `202 accepted` · `200 replay (idempotent by decision_id)` ·
`400 validation_failed` · `401 bad signature` · `413 too_large`.
The connector renders exactly one completed "Agent Verification Gate" check
run per accepted envelope: verb→conclusion is
`eligible_for_merge_train→success`, `rejected→failure`, `deferred→neutral`;
unknown verbs fail closed. Without GitHub App credentials the connector runs
in dry-run mode, logging the would-be payload instead of calling the API.
