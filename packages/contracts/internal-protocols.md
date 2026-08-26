# CISync Internal Protocols (FROZEN v1)

Inter-service HTTP contracts not exposed in openapi.yaml. Binding for W1/W2 builders.

## 1. ingest → control-plane (webhook forwarding)

`POST /internal/ctrl/deliveries`

Headers: `Idempotency-Key: <ext_delivery_id>`, `X-CISync-Signature: sha256=<hex hmac of raw body with shared secret>`, `Content-Type: application/json`.

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

Base: `CISYNC_CTRL_FLEET_URL`. Fleet NEVER reads the ledger; control-plane drives it.

### Job-lease credentials (THREAT_MODEL B2 / I-04)

Control-plane mints ONE Ed25519-signed job-lease token per dispatched run at
dispatch time (`alg: EdDSA`, compact JWT). Claims:

| claim | value |
|---|---|
| `aud` | `"cisync-fleet"` |
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

Fleet public key envs: `CISYNC_FLEET_JOBLEASYPUB_KEY_FILE` (PEM file) or
`CISYNC_FLEET_JOBLEASE_PUB_B64` (base64 inline PEM). Control-plane signs with
its dedicated key from `CISYNC_CTRL_JOBLEASE_KEY_FILE`. Unconfigured fleets
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

## 4. control-plane → github-connector (decision push, W2; WIDENED W5)

Base: `CISYNC_CTRL_CONNECTOR_URL`. The connector is idle-until-fed;
control-plane pushes envelopes via its outbox relay. ONE endpoint serves
THREE envelope kinds discriminated by `kind`; absent `kind` decodes as
`decision` (v1 relay compatibility).

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
  Headers: `Idempotency-Key: <kind-dependent, below>`,
  `X-CISync-Signature: sha256=<hex hmac of raw body with CISYNC_CONN_WEBHOOK_SECRET>`,
  `Content-Type: application/json`. Body cap 1 MiB.

### 4.1 kind = "decision" (completed verdict)

```json
{
  "kind": "decision",
  "decision_id": "dec_01J…",
  "candidate_id": "cand_01J…",
  "repo": "acme/payments",
  "head_sha": "…40 hex…",
  "verb": "eligible_for_merge_train|rejected|deferred",
  "confidence": 0.94,
  "policy": {"policy_id": "pol_…", "policy_version": 4},
  "summary": "explanation summary",
  "rendered_at": "RFC3339",
  "evidence": {"required": 5, "accepted": 5, "deferred": 2, "failed": 0},
  "annotations": [{"path":"pkg/x.go","start_line":42,"message":"…","kind":"api_compat"}]
}
```

W5 deltas (G6/G8): `evidence` counts `{required, accepted, deferred,
failed}` — EXACT field names above, all non-negative ints, and
`accepted ≤ required`, `deferred + failed ≤ required` — let the connector
render the dossier census into the check summary instead of scraping
projections. `annotations` carries failed-required-kind findings for GitHub
failure annotations: `path` optional (omitted ⇒ file-level message),
`start_line` omitted when absent/0, `message` + `kind` REQUIRED.
`evidence`/`annotations` are OPTIONAL blocks; an envelope without them keeps
the v1 flat-summary rendering. `Idempotency-Key` MUST equal `decision_id`.

### 4.2 kind = "lifecycle" (queued / in_progress)

```json
{"kind":"lifecycle","phase":"queued|in_progress","candidate_id":"cand_01J…",
 "repo":"acme/payments","head_sha":"…40 hex…","at":"RFC3339"}
```

Emitted from existing outbox events: `candidate.submitted` ⇒ `queued`;
FIRST `validation.started` per candidate ⇒ `in_progress`. `at` stamps the
transition time (byte-stable dry-run rendering). `Idempotency-Key` MUST
equal `"<candidate_id>:<phase>"` — deterministic, so relay redeliveries
collapse without connector-side state. Effect: the connector creates the
check run on `queued` and UPDATES THE SAME RUN on `in_progress`
(`Checks.UpdateCheckRun`) — one check run per candidate revision walking
`queued → in_progress → completed`. Every check run the connector creates
or updates carries `external_id = candidate_id` (NOT decision_id) so GitHub
re-runs map back to the revision regardless of which decision it carried.

### 4.3 kind = "rerun_requested"

```json
{"kind":"rerun_requested","candidate_id":"cand_01J…","repo":"acme/payments",
 "head_sha":"…40 hex…","requested_by":"github:<login>?","requested_at":"RFC3339"}
```

Relayed when a `check_run.rerequested` webhook's `external_id` matches one
of our candidates. `requested_by` is display-only provenance (§2.2).
`Idempotency-Key` MUST be the originating GitHub `ext_delivery_id`.
Connector policy: `CISYNC_CONN_RERUN_POLICY ∈ {replan, replay_cached}`
(default `replan`); caps `CISYNC_CONN_RERUN_MAX_PER_CANDIDATE=2`,
`CISYNC_CONN_RERUN_RATE_PER_HOUR=20`. Over-cap or ctrl-unreachable ⇒ the
check flips to a VISIBLE neutral ("budget exhausted" / "unavailable") — a
required check never silently ignores a re-run. Unknown candidate ⇒ typed
`404 unknown_candidate`.

### 4.4 revalidate command (connector → control-plane)

- `POST {ctrl}/v1/candidates/{id}/revalidate`
  Headers: `Authorization: Bearer <admin token>` (same token model as other
  admin-auth'd internal routes), `Content-Type: application/json`,
  `Idempotency-Key: <originating GitHub ext_delivery_id>` (REQUIRED,
  16..128 chars per openapi `IdempotencyKey` — ctrl dedupes via command_log,
  replays return the ORIGINAL 202 body), empty `{}` body.
  → `202 {"plan_id": "plan_…"}` · `400 validation_failed` (missing/short
  Idempotency-Key, malformed JSON) · `401 unauthorized` ·
  `404 unknown_candidate` · `409 conflict_state` with
  `details.reason = "rerun_budget_exhausted"` (per-candidate revalidation
  cap spent OR candidate already terminal — a permanent verdict, race-safe
  conditional UPDATE) · `503 unavailable`.
  Appends a re-plan command under CURRENT policy + current inputs_hash;
  the SAME candidate revision continues (rerun_count++) so lifecycle
  envelopes keep `candidate_id` stable and the check run identity holds.
  When `CISYNC_CONN_CTRL_URL` is unset the replan feature is flag-OFF and
  re-runs surface as neutral "unavailable".
  Connector-side mapping of non-202 answers: `404` relays as typed
  `unknown_candidate`; `409` flips the check to a VISIBLE neutral
  "re-run budget exhausted"; every other failure (network, 401, 5xx)
  flips it to a VISIBLE neutral "unavailable" — never silent.

Responses for all kinds: `202 accepted` (`{"accepted":true,"dry_run":bool,
"queued"?,"outcome"?}`) · `200 replay (idempotent)` ·
`400 validation_failed` · `401 bad signature` · `404 unknown_candidate`
(rerun) · `413 too_large` · `503 unavailable (storage; redeliver)`.
The connector renders verb→conclusion `eligible_for_merge_train→success`,
`rejected→failure`, `deferred->neutral`; unknown verbs fail closed. Without
GitHub App credentials the connector runs in dry-run mode, logging the
would-be payload instead of calling the API. Local write budget
(`CISYNC_CONN_WRITE_BUDGET_PER_HOUR=300`/installation/hour) exhaustion
QUEUES writes outbox-style (`ghconn.pending_writes`) and drains later —
never silently drops a required check. Stalled non-completed checks older
than `CISYNC_CONN_STALLED_CHECK_AGE` (default 45m) flip to neutral.
