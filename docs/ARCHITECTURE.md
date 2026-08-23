# Sauron Architecture (FROZEN v1)

> Normative. Builder agents implement against THIS file + `packages/contracts/`.
> `docs/plans/*` are historical planning inputs; where they conflict, this file wins.

## 0. Mission

Sauron converts an unbounded stream of agent-generated code changes into prioritized,
deduplicated, evidence-backed validation runs and renders explainable merge/reject/
repair decisions over an append-only, tamper-evident decision ledger.

## 1. Reconciliation decisions (planning-wave synthesis)

| # | Decision |
|---|---|
| D1 | **No Redis in v1.** Postgres is the sole state authority. Dedup = unique constraints; rate limits = token-bucket table; WIP caps = indexed counts; leases = TTL rows + sweeper. All behind small Go interfaces (`Deduper`, `RateLimiter`, `BudgetStore`) so Redis/NATS slot in later. |
| D2 | **Ingest (:8080) is GitHub-webhooks-only.** Agent/human REST API is served directly by control-plane (:8081). No proxy hop. |
| D3 | **github-connector ships minimal in v1**: read-only consumer that writes one static "Agent Verification Gate" GitHub Check from `decision.rendered`. Full connector (diff reads, merge queue, comments) = W3+. |
| D4 | **Event names:** `<aggregate>.<verb_past>` lowercase dot-notation (e.g. `candidate.submitted`). Canonical list lives in `packages/contracts/events.schema.json`; CORE set is binding for v1. |
| D5 | **Unified lease aggregate** (`lease_`, `scope.kind = change_scope \| environment`). |
| D6 | **Repair resubmission re-enters the SAME candidate** into `validating` (revision chain deferred to W3). |
| D7 | **Policy source v1:** compiled-in default policy pack (see DOMAIN_MODEL_DRAFT §8 shape). Repo adapter YAML parsing = LATER (interface stub only). |
| D8 | **Evidence sufficiency %** = `len(accepted ∩ required_kinds) / len(required_kinds)`; rendered alongside decision confidence, never conflated. |
| D9 | **IntegrationSet is NOT a v1 aggregate** (reserved in Decision.subject enum). Merge trains = W3. |
| D10 | **Ledger checkpoint key:** Ed25519 private key held ONLY by control-plane (dev: env/file-mounted key; prod: KMS/HSM later). Chain verification job fails closed on mismatch. |
| D11 | **Tenancy:** single-tenant demo posture, but `tenant_id` stamped on every row/event, query predicates from auth token only, uniform 404s — cheap insurance, never retrofittable. |
| D12 | **Synthetic intents** (PR without declared intent) auto-create with `origin=github_webhook`, visibly flagged in UI. |

## 2. Services & ownership map

Ports: ingest **8080** · control-plane **8081** · runner-fleet **8082** · web **3000** · postgres **5432**.
DB: single Postgres 16 database `sauron`, schema-per-service ownership (exclusive write):

| Service | PG schema | Owns (tables) |
|---|---|---|
| ingest | `ingest` | `deliveries` |
| control-plane | `ctrl` | `ledger`, `ledger_checkpoints`, `outbox`, `command_log`, `processed_events`, projections: `intents`, `candidates`, `clusters`, `cluster_members`, `validation_plans`, `validation_runs`, `evidence_records`, `failure_cases`, `repair_tasks`, `leases`, `policies`, `decisions`, `rate_limits`, `stats_test_outcomes` |
| runner-fleet | `fleet` | `workers`, `execution_jobs`, `artifacts` |
| github-connector (W2) | `ghconn` | `installations`, `check_reports` |

Migrations: each service owns `services/<svc>/migrations/NNNN_snake_desc.{up,down}.sql`.
NO service may SELECT against another service's schema. Cross-service data flows only
via the outbox→relay event stream or explicit HTTP.

### Go module layout (binding)

```
services/<svc>/
├── cmd/<svc>/main.go          wiring only
├── internal/api/              HTTP handlers (<resource>_handler.go)
├── internal/domain/           pure types/state machines, zero I/O
├── internal/store/            pgx persistence (one file per aggregate)
├── internal/<component>/      svc-specific (scheduler/, evidence/, relay/, providers/ …)
├── internal/config/config.go  env parsing (SAURON_<SVC>_<VAR>)
├── migrations/
├── go.mod                     module sauron.dev/sauron/<svc>
└── Dockerfile
```

control-plane internal components (extractable post-v1, import rules enforced):
`domain/ store/ planner/ scheduler/ evidence/ cluster/ failure/ policy/ lease/ api/ relay/ verify/`.

Allowed deps: `jackc/pgx/v5`, `oklog/ulid/v2`, `google/go-github/v66` (W2),
`pgregory.net/rapid` (tests), `testify` (tests), `golang-migrate/migrate` (CLI-side).
HTTP: stdlib `net/http` ServeMux only. No frameworks.

## 3. Runtime model

- **Sync surface (blocking):** control-plane REST (intents/candidates/dossier/leases).
- **Async spine:** every mutation appends `ctrl.ledger` + `ctrl.outbox` in ONE tx;
  relay goroutine `SELECT … FOR UPDATE SKIP LOCKED ORDER BY id LIMIT 100`,
  wakes on `NOTIFY outbox_changed`, 500 ms poll fallback. At-least-once delivery;
  consumers dedupe via `processed_events(event_id PK)` inside their effect tx.
- **Scheduler loop (per repo shard):** scan runnable runs `(state='queued') ORDER BY
  effective_priority DESC`, admit under WIP caps + budgets (atomic reservation),
  dispatch to runner-fleet claim endpoint, fence-tokened.
- **Runner protocol:** fleet polls `POST /internal/fleet/jobs/claim` (capability filter)
  → executes via Provider (`sim` in-proc | `docker`) → heartbeats → uploads result +
  logs digest BEFORE ack → complete accepts only current fence token.
- **Reconciler (30 s):** expire leases, kill+fence expired executions, rebuild stale
  projections from ledger tail, close ledger chain checkpoints (every 10k events).

## 4. Data-flow walkthroughs

See ARCHITECTURE_DRAFT §3 (a/b/c) — webhook→decision, intent→dossier,
duplicate-clustering→supersede propagation. Those traces remain normative.

## 5. Trust requirements binding all builders

1. Ledger tables are INSERT/SELECT-only for services (DB trigger rejects UPDATE/DELETE);
   corrections append compensating events.
2. Runners submit DATA; control-plane authors FACTS. Evidence records signed (Ed25519)
   by control-plane at accept-time. Job-lease tokens asymmetric-signed, fenced, ≤60 m TTL,
   one accepted evidence record per jti.
3. Sandboxes receive code+fixtures ONLY — no installation tokens, no DB creds, no signing
   keys; egress default-deny (sim provider enforces trivially by construction).
4. Redaction middleware before persisting any external payload/log; fail-closed scrubber.
5. Tenant predicate in every query; tenant_id derived from token, never payload.
6. Cache/artifact reuse keys include FULL inputs_hash (base SHA, lockfiles, flags,
   toolchain); skipped ≠ pass; changed input ⇒ miss.
7. Docker provider labeled NOT-FOR-PRODUCTION until THREAT_MODEL §graduation passes.

## 6. v1 cut line

IN: 4 services (connector idle-until-W2 binary), webhook ingest+HMAC+dedup, agent API
(12 CORE endpoints), clustering v0 (path-overlap ≥0.6 + trigram), relations + supersede
propagation, priority scheduler + Tier 0–2 ladder via sim/docker providers, failure
taxonomy subset (infra_transient retry, deterministic_regression → bounded repair
authorization), hash-chained ledger + projections + invalidation on merge-base advance,
evidence dossier + decisions + explanations, web Change-Graph UI, minimal GH check-writer
(W2 within v1), idempotency + backpressure (429 budget_exceeded shape), reconciler,
chain verifier, storm simulator green at 500 concurrent candidates.

OUT: merge trains/integration sets, autonomous merge/deploy authority, canary loop,
ML test selection (heuristics only), flake quarantine automation, k8s/firecracker
providers (interfaces only), SSO/RBAC beyond admin token, MCP/SDKs.

## 7. Verification gates (every wave)

`make hygiene` (fmt/lint/tsc/structure) · `go vet ./...` · `go test ./...` (unit+property)
· `make up && make test-integration` (black-box compose suites) · storm scenario asserts.
A wave is DONE only when its gates pass and REPO_STANDARDS §6 conduct rules were followed.
