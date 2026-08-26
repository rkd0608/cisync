# CISync — Architecture Draft (v1)

Status: DRAFT · Owner: Chief Systems Architect · Scope: v1 vertical slice + scaling path
Inputs: product spec (`CI_CD and build systems Most pipelines were design.md`) + working defaults from planning brief.

---

## 1. System overview

Sauron is the **verification and integration control plane for agent-generated code**: it converts an unbounded stream of agent changes (intents, patches, PRs) into a bounded set of prioritized, deduplicated, evidence-backed validation runs, and renders explainable merge/reject/repair decisions. It is not another CI executor competing with Actions/Buildkite; it is the scheduling brain and system of record above any execution substrate. The durable moat is the append-only, hash-chained **decision ledger** and its projections: the verification graph (intent → candidate → cluster → evidence → decision).

```text
                     FULL PLATFORM (target)          [v1 = solid, Wave 2+ = dashed]
 GitHub/GitLab ─webhooks→ ┌────────┐  commands  ┌───────────────┐
 Coding agents ─API/MCP─► │ ingest │ ─────────► │ control-plane │
 Humans ─Next.js UI◄──read models─── │            │ domain·sched· │
                                     └──────────► │ evidence·lease│
                                                  └──────┬────────┘
                              dispatch ▲─────────────────┘
                                 ┌─────┴───────┐
                                 │ runner-fleet│   providers:
                                 └─────┬───────┘   simulator · docker · - - k8s - -
                                       │           - - firecracker - -
                            artifacts/logs/results
 - - ┌──────────────────┐
     │ github-connector │◄─ events (checks/status write-back, diff reads, merge queue)
     └──────────────────┘

 Storage: Postgres 16 — schemas ingest|control|fleet|ghconn (system of record)
          Redis 7    — dedup windows, leases, rate limits (rebuildable state)
```

---

## 2. Service decomposition

### 2.1 `ingest` — edge + normalization
- **Responsibility:** terminate all external ingress (GitHub webhooks, agent API, CLI). Verify signatures, dedupe deliveries, normalize external payloads into canonical commands/events. Holds no domain state.
- **Owned data:** PG schema `ingest`: `deliveries(id ULID PK, source, ext_delivery_id, sig_ok bool, headers jsonb, payload jsonb, received_at, status)` — raw audit anchor.
- **Exposed APIs:** `POST /v1/hooks/github`; agent API proxied to control-plane synchronously: `POST /v1/intents`, `POST /v1/intents/{id}/candidates`, `GET /v1/candidates/{id}/dossier`, `GET /healthz`. All mutating endpoints require `Idempotency-Key`.
- **Consumed events:** none (pure edge).
- **Scaling:** stateless, N replicas behind LB; Redis dedup window (`SETNX dedup:{source}:{ext_delivery_id} EX 3600`) backed by PG unique `(source, ext_delivery_id)` as fallback.
- **Failure behavior:** bad signature → 401 + logged row; control-plane down → 503 + `Retry-After` (GitHub re-delivers); Redis down → PG constraint path; overload → 429 per §5 backpressure.

### 2.2 `control-plane` — the brain
- **Responsibility:** all domain logic: intent/candidate/cluster/evidence state machines, admission control, priority scheduler (`priority(v) = P(decision changes)·risk-reduction·urgency·value / cost`), tier ladder (Tier 0–4), lease authority, failure taxonomy + bounded repair authorization, decision rendering with explanations.
- **Owned data:** PG schema `control`: `ledger` (append-only hash chain), `outbox`, `command_log`, plus projection tables: `intents`, `candidates`, `clusters`, `cluster_members`, `validation_requests`, `evidence_records`, `failure_cases`, `repair_tasks`, `decisions`. Redis keys: `lease:{surface}` sets, `ratelimit:{tenant}`, `wip:{tier}`.
- **Exposed APIs:** internal command API (`POST /commands` with `Idempotency-Key`), read API for web/dossier (`GET /v1/intents/*`, `/candidates/*`, `/clusters/*`), `GET /metrics`.
- **Consumed events:** everything in the ledger (it subscribes to its own outbox for reactor-style handlers), plus fleet execution results.
- **Scaling:** single logical service, internally partitioned by `repo_id` hash — each repo's event stream is processed serially (per-aggregate ordering guarantee); repos distribute across workers. Scheduler runs per-shard with a global arbiter pass for scarce resources (env templates, Tier 3 budget).
- **Failure behavior:** crash-safe by design — state machines rebuild from ledger replay; idempotent handlers; a stuck shard halts only that repo's progress and alerts.

### 2.3 `runner-fleet` — execution
- **Responsibility:** claim `ValidationRequested` work, execute via provider adapters, stream heartbeats, upload logs/artifacts, emit completion events, enforce cancellation. Provider interface now; simulator + docker implemented; k8s/firecracker later.
- **Owned data:** PG schema `fleet`: `workers(id, pool, capacity, last_heartbeat)`, `execution_jobs(id, validation_request_id, provider, status, epoch int, claimed_at, finished_at, result_ref)`, `artifacts(digest sha256 PK, kind, size, storage_key, produced_by_job)`.
- **Exposed APIs:** worker protocol `POST /fleet/jobs/claim`, `POST /fleet/jobs/{id}/heartbeat`, `POST /fleet/jobs/{id}/complete`; `Provider` Go interface: `Submit/Cancel/Poll`.
- **Consumed events:** `validation.requested`, `validation.cancelled`, `lease.revoked`.
- **Scaling:** stateless dispatchers; horizontal pools per provider; queue depth gauge `fleet_queue_depth{pool,tier}` exported for autoscaler hooks (KEDA/HPA later).
- **Failure behavior:** heartbeat timeout (>2× interval) → job epoch++ and reclaim by another worker (fencing: stale epochs rejected at complete-time); provider crash → `execution.failed(class=infra_transient)`; results always uploaded before ack.

### 2.4 `github-connector` — Wave 2
- **Responsibility:** outbound GitHub surface: post/update the **Agent Verification Gate** check from `DecisionRendered`, read diffs/changed-files, merge-queue awareness, PR comment threads. Read-only contents; no code write-back in v1 era.
- **Owned data:** schema `ghconn`: `installations`, `check_reports(candidate_id, check_run_id, state, updated_at)`.
- **Exposed APIs:** none externally; consumes ledger events.
- **Scaling/failure:** stateless consumer; GitHub API rate limits → token-bucket per installation, exponential backoff; missed updates reconciled by periodic diff against open candidates.

### 2.5 `apps/web` (Next.js + TS)
Read-mostly Change Graph UI: intents, clusters, evidence dossiers, decisions with explanations, queue/capacity views. Writes go through the same agent API (no privileged DB access). Scales horizontally; reads projections via control-plane read API, not direct DB.

---

## 3. Data flow walkthroughs

### (a) Webhook PR opened → validation decision
1. GitHub delivers `pull_request.opened` → `ingest`: verify HMAC, dedupe on delivery GUID, persist `deliveries`, emit `DeliveryAccepted(source=github, kind=pr_opened)`.
2. `control-plane` normalizer maps PR → candidate. If no intent exists, creates **synthetic intent** (`actor=github`, goal extracted from title/body, surfaces = changed paths).
3. Emits `intent.declared` + `candidate.submitted(patch_ref=diff_url, base_sha, head_sha)`.
4. Clusterer assigns cluster (path-overlap + similarity): `cluster.assigned(relation=...)`.
5. Policy engine builds plan: `validation.planned(tiers=[T0,T1], tests=selected, rationale="no dep-path to ui/**")`.
6. Scheduler admits within WIP budget: `validation.requested(request_id, tier, est_cost, cancellation_conditions)`.
7. `runner-fleet` claims → executes (docker) → `validation.started` … `validation.completed(status, logs_digest, artifact_digests[])`.
8. Evidence evaluator: pass → `evidence.recorded(kind=tier1, verdict=pass)`; fail → `failure.classified(taxonomy=…)` → policy: infra_transient → bounded retry; deterministic regression → `repair.authorized(max_iterations=2, allowed_paths=[...])`.
9. When plan satisfied: `decision.rendered(verb=eligible_for_merge_train|rejected|deferred, confidence, policy_version, explanation)`. Wave 2: `github-connector` publishes check.

### (b) Agent declares intent → lease → candidate → dossier
1. Agent `POST /v1/intents {goal, repository, base, expected_surfaces, acceptance_criteria, risk}` with `Idempotency-Key`. `ingest` proxies synchronously to `control-plane /commands`.
2. Admission: overlap search over active intents' owned surfaces (Redis `lease:{surface}` sets). Conflicts returned inline (`relation=overlapping`, recommendation=coordinate).
3. Single tx: `command_log` insert + `intent.declared` + `lease.granted(scope=surfaces+budget{cpu_min, env_min, repair_attempts}, ttl=25m)` appended to `ledger` + `outbox`.
4. Response returns `intent_id`, `lease_id`, `base_snapshot`, `allowed_paths`, `conflicts[]`, `required_evidence[]`, `compute_budget`.
5. Agent implements, then `POST /v1/intents/{id}/candidates` (patch ref: git bundle URL or bundle digest) → `candidate.submitted` → clustering → `validation.planned` → ladder runs as in (a).
6. Dossier: `GET /v1/candidates/{id}/dossier` renders from projections: accepted evidence, deferred evidence *with reasons*, known uncertainty, required post-merge evidence, decision + confidence + policy version.
7. Lease TTL sweeper emits `lease.expired` if not renewed; renewal is a heartbeat command.

### (c) Duplicates → clustering → superseding/cancellation propagation
1. Candidate B arrives; clusterer computes relation vs existing cluster members (changed-symbol overlap ≥ θ, embedding cosine, dependency-subgraph intersection): `cluster.assigned(cluster_id=C, relation=duplicate_of=A)`.
2. Scheduler keeps representative A (argmax priority); B → `blocked_representative`, no expensive tiers purchased.
3. A completes successfully → `decision.rendered(eligible)` → `candidate.superseded(candidate=B, by=A, reason=dominated_duplicate)` for each sibling.
4. Propagation: control-plane cancels B's queued requests (`validation.cancelled`), revokes B's lease (`lease.revoked`), runner-fleet cancels any in-flight execution via `Provider.Cancel`, agents notified via `GET` poll/webhook flag on next sync.
5. Ledger preserves everything: B's partial evidence stays attributed (never deleted — append-only), available for reuse (`artifact.reused`, RESERVED v1).
6. Inverse path: A later fails deterministically → `representative.promoted(next_best=B)` (RESERVED) or cluster re-evaluated; supersede decisions carry `causation_id` back to the triggering evidence for auditability.

---

## 4. Event taxonomy & envelope

### 4.1 Envelope (single JSON shape for all ledger entries)

```json
{
  "id": "evt_01JAXXXXXXXX",            // ULID, globally unique
  "seq": 10482,                        // global ledger position (PG sequence)
  "type": "candidate.submitted",       // <aggregate>.<verb>, past tense
  "version": 1,
  "tenant_id": "org_01J...",
  "aggregate": { "type": "candidate", "id": "cand_01J..." },
  "causation_id": "evt_01J...",        // event/command that directly caused this
  "correlation_id": "corr_01J...",     // root: original delivery or intent
  "actor": { "kind": "agent|human|service|github", "id": "agent_01J..." },
  "occurred_at": "2026-08-23T03:41:00Z",
  "payload_sha256": "…",
  "prev_hash": "sha256 of previous ledger entry",
  "entry_hash": "sha256(seq‖id‖type‖version‖payload_sha256‖prev_hash)"
}
```

Tamper-evidence: per-ledger SHA-256 hash chain; a signed checkpoint row (`ledger_checkpoints(seq, entry_hash, sig)`) every 10k events lets verifiers skip O(n) full-chain replay. Verification job recomputes chain on read replicas nightly.

### 4.2 Canonical events

| Event | Semantics | Status |
|---|---|---|
| `delivery.accepted` | raw external payload persisted + normalized | CORE |
| `intent.declared` | agent/human declares goal, constraints, surfaces, risk class | CORE |
| `lease.granted` / `lease.revoked` / `lease.expired` | scoped change/env lease lifecycle with TTL and budget | CORE |
| `candidate.submitted` | concrete patch registered against an intent | CORE |
| `cluster.assigned` | candidate placed in cluster with relation (duplicate/alternative/composable/conflicting) | CORE |
| `validation.planned` | tiered evidence plan chosen w/ selection rationale | CORE |
| `validation.requested` | scheduler admits one validation unit within budget | CORE |
| `validation.cancelled` | queued/in-flight validation voided by supersede/staleness | CORE |
| `validation.started` / `validation.completed` | execution lifecycle from runner-fleet | CORE |
| `evidence.recorded` | accepted evidence attached (kind, verdict, digests, cost) | CORE |
| `evidence.invalidated` | prior evidence voided (merge-base moved, toolchain changed) | CORE |
| `failure.classified` | taxonomy class assigned (infra_transient, flake, deterministic_regression, …) | CORE |
| `repair.authorized` / `repair.completed` | bounded repair envelope granted/closed within max_iterations | CORE |
| `candidate.superseded` / `candidate.cancelled` | domination/cancellation with reason + causation | CORE |
| `merge_base.advanced` | base branch moved → staleness invalidation trigger | CORE |
| `decision.rendered` | final verb + confidence + policy version + explanation | CORE |
| `github.check_published` | Agent Verification Gate written back | RESERVED (W2) |
| `artifact.reused` | evidence/artifact shared across cluster members | RESERVED |
| `test.flake_suspected` / `flake.quarantined` | flake detection & quarantine workflow | RESERVED |
| `representative.promoted` | cluster re-elects representative after failure | RESERVED |
| `integration_set.created` / `integration.verified` | merge-train composition & verification | RESERVED (W3) |
| `deploy.started` / `canary.observed` / `rollback.triggered` / `outcome.recorded` | post-merge production loop | RESERVED (W3+) |
| `policy.override_requested` | human exception flow | RESERVED |

---

## 5. Inter-service communication

**Sync vs async rule:** anything an external caller must block on (agent intent/lease grant, dossier reads) is synchronous HTTP (`ingest → control-plane /commands`, p99 < 200ms); everything internal is async via the ledger. No service-to-service event buses besides PG.

**Transactional outbox + polling/publish:** every state transition appends `ledger` rows **and** `outbox(id, status, attempts, next_attempt_at)` rows in one tx. A relay goroutine: `SELECT … FOR UPDATE SKIP LOCKED ORDER BY id LIMIT 100`, publishes to in-process subscribers (fleet dispatcher, connector), marks `published`. PG `NOTIFY outbox_changed` wakes the relay for low latency; polling every 500ms is the correctness fallback — NOTIFY is an optimization, never the source of truth. At-least-once delivery; consumers dedupe.

**Idempotency everywhere:**
- Webhooks: unique `(source, ext_delivery_id)` + Redis window.
- Agent commands: `Idempotency-Key` → `command_log(key PK, request_hash, response_ref)`; replays return cached response.
- Consumers: `processed_events(event_id PK)` guard + projection upserts keyed by `(aggregate_id, seq)`.
- Runner claims: fencing epoch — results from stale epochs rejected.
- Outbox relay: SKIP LOCKED + status transitions make multi-replica safe.

**Backpressure when arrivals exceed capacity:**
1. Edge admission: Redis token bucket per tenant (`ratelimit:{tenant}`, refill = contracted rate) → 429 + `Retry-After`.
2. Queue-depth circuit breaker: if `outbox` depth > 10k or `pending_validations > threshold`, ingest degrades webhook classes (drops non-required PR synchronize events to "record only") before dropping agent commands.
3. Scheduler WIP caps per tier (`wip:{tier}` in Redis) — Tier 3/4 scarce capacity explicitly rationed by priority, not FIFO.
4. Fleet queue depth drives autoscaler hook (`fleet_queue_depth` gauge); v1 simulator scales horizontally, docker pool capped by host resources.
5. Agents see honest signals: lease grants carry `queue_position` and `eta`; candidates can be `deferred` rather than silently slow.

---

## 6. Storage design

### 6.1 Table inventory

| Schema | Tables (key columns beyond ids/timestamps) | Notes |
|---|---|---|
| `ingest` | `deliveries` | immutable raw payloads, 30d retention |
| `control` | `ledger(seq, type, aggregate_type, aggregate_id, causation_id, correlation_id, actor jsonb, payload jsonb, payload_sha256, prev_hash, entry_hash)` · `outbox(status, attempts, next_attempt_at)` · `command_log` · `ledger_checkpoints` | system of record |
| `control` projections | `intents(state, risk_class, surfaces text[], deadline)` · `candidates(intent_id, state, patch_ref, base_sha, head_sha, priority numeric)` · `clusters(repo_id, rep_candidate_id)` · `cluster_members(cluster_id, relation)` · `validation_requests(candidate_id, tier, state, est_cost, cancellation_conditions jsonb)` · `evidence_records(request_id, kind, verdict, digests, confidence)` · `failure_cases(class, confidence, repro, suspected_paths)` · `repair_tasks(max_iterations, attempts, allowed_paths)` · `decisions(verb, policy_version, explanation jsonb)` | all rebuildable from ledger |
| `fleet` | `workers` · `execution_jobs(validation_request_id, provider, status, epoch, result_ref)` · `artifacts(digest PK, storage_key)` | digest-addressed artifacts |
| `ghconn` | `installations` · `check_reports` | Wave 2 |

### 6.2 Indexing strategy
- Ledger: btree `(aggregate_type, aggregate_id, seq)` (stream replay), `(correlation_id)` (trace assembly); monthly range partitions on `seq`.
- Candidates/intents: `(repo_id, state)` partial where active; GIN on `surfaces text[]` for overlap search (admission hot path); `(cluster_id)` on members.
- Validation requests: `(state, tier, priority DESC)` partial where queued — this is the scheduler's scan; keep it index-only.
- Deliveries: `(source, ext_delivery_id)` unique; brin on `received_at`.
- Artifacts: digest PK; `(kind, produced_by_job)`.

### 6.3 Retention
Ledger + checkpoints: infinite (cheap rows, partitioned). Projections: rebuildable, vacuum-friendly. Deliveries 30d; execution logs 14d (digest + structured summary kept forever); artifact payloads 30d (digests forever); `processed_events` 90d.

### 6.4 Why event-sourced + projections, not CRUD
1. The ledger **is the product**: auditable, tamper-evident provenance for every accept/reject/repair decision — impossible to reconstruct retroactively under CRUD.
2. Replay enables new intelligence (test-selection model, shadow-mode counterfactuals) without backfill pipelines.
3. Causation/correlation chains make "why did this run happen" answerable — the trust mechanism spec demands.
4. Projections are disposable caches; schema evolution = new projection + replay, no risky migrations on history. Cost accepted: write amplification (2 rows per change) and eventual-consistency reads — both cheap here.

---

## 7. Scaling path

| Load | First thing to break | Designed answer |
|---|---|---|
| ~1k concurrent candidates (10x demo) | single-threaded outbox relay; scheduler full-rescan | sharded relays (hash by aggregate); incremental priority heaps per repo-shard; pgbouncer in front of PG |
| 10k candidates/day, 100 repos (10x prod) | PG connection count; overlap-search latency on surfaces | pgbouncer transaction pooling; GIN-indexed surface sets + Redis cache of active-surface map |
| 100x arrivals | PG write volume (ledger+outbox); global scheduler contention | shard control-plane fully by `tenant/repo`: disjoint repo sets per shard own their schemas' partitions; bus interface (`pubsub.Bus`) cutover to NATS JetStream behind same contract; ledger partitioned per tenant |
| 100x execution | docker host saturation; long queues | k8s/firecracker providers implement existing `Provider` iface; warm pools per workload class; KEDA on `fleet_queue_depth`; artifact payloads move to S3 content-addressed store |
| Popular-repo hot keys | Redis lease/ratelimit contention on one repo's surfaces | slot-partition lease keys by surface hash; local in-proc cache with lease-version tokens |

Invariant preserved at every stage: per-aggregate ordering + exactly-once *effects* via idempotency, never assumed exactly-once delivery.

---

## 8. Technology decisions record

| Choice | Alternatives | Rationale | Risk |
|---|---|---|---|
| **Go 1.23 for core services** | Rust; TypeScript end-to-end | Workload is IO-bound orchestration (webhooks, scheduling, DB) — goroutines + stdlib `net/http` ServeMux are ideal; fast iteration with AI-assisted coding; single static binaries. Rust reserved for a future Firecracker supervisor where per-job overhead and memory safety matter most. TS confined to web UI | Go's type system weaker for rich domain models — mitigated by explicit state machines + exhaustive enums via codegen from `packages/contracts/events` |
| **Postgres-as-bus** (outbox+NOTIFY+poll) | NATS JetStream now; Kafka | Zero extra v1 infra in docker-compose; transactional consistency between state and events for free; ≤ few-k msg/s well within PG. `pubsub.Bus` interface defined day 1 so NATS is a cutover, not a rewrite | LISTEN/NOTIFY unreliability under failover — polling fallback covers it; PG becomes scaling bottleneck by design (§7) |
| **pgx/v5 (+sqlc for query gen)** | lib/pq, GORM/ent | Native PG types, batching, COPY, LISTEN support, prepared statements; sqlc keeps hot-path SQL reviewable | sqlc codegen churn during schema iteration — acceptable pre-1.0 |
| **ULID identifiers** (`evt_`, `cand_`, …) | UUIDv4, UUIDv7, Snowflake | K-sortable → index-local inserts, time-ordered without coordinator, human-distinguishable prefixes aid debugging/UX | 26-char text size vs 16-byte binary — store as `text`, fine at our volumes |
| **SHA-256 hash-chained ledger** | Merkle tree; external notarization (S3 Object Lock, RFC-3161) | Simple incremental verification; checkpoints bound replay cost; sufficient tamper-evidence for v1 trust story | Chain integrity depends on write-path compromise — checkpoint signing key custody is an open question (§9) |
| **Redis 7 for leases/dedup/ratelimit** | Postgres-only (advisory locks + TTL table) | TTL-native atomic ops fit leases exactly; keeps hot admission path off the ledger DB | Second datastore to operate; all Redis state rebuildable — PG fallback documented if we want one fewer dep in demos |
| **Next.js + TS web** | Go templates; Remix | Dashboard velocity, component ecosystem, hiring | Server-component complexity — read-mostly app keeps it simple |
| **In-process simulator + docker providers first** | K8s Jobs now | Deterministic tests, zero cloud deps for v1 slice; provider interface frozen early so k8s/firecracker slot in later | Docker-in-CI quirks on macOS — simulator is the CI-default provider |

---

## 9. Open questions (for other planners)

**Security:** agent identity model for v1 (static API keys → OIDC later?); webhook secret rotation procedure; threat model for executing agent patches in docker (network egress off by default?); who holds/custodies the ledger checkpoint signing key; tenant isolation bar for v1 (shared demo instance with `tenant_id` rows vs strict single-tenant).
**SRE:** PG HA/backup RPO for the ledger (it's irreplaceable, unlike projections); chain-verification job cadence + alerting on mismatch; compose→cloud migration owner and target (Fly/Railway/ECS?); log pipeline for execution output (stdout vs object store).
**Domain:** clustering similarity definition sign-off — v0 is path-overlap ≥ θ + trigram similarity; when do embeddings enter and whose vendor? Policy DSL: is the repo adapter YAML (`.integration/agent-control.yaml`) the v1 policy source or do we ship a built-in default policy only? What are the exact inputs to "evidence sufficiency %" for the dossier v1? Synthetic-intent UX for unattributed human PRs — auto-create silently or require confirmation?

---

## 10. v1 cut line

**IN**
- 4 Go services (github-connector compiled but idle) in docker-compose: PG16 + Redis7.
- Agent API: intents, leases, candidate submission, dossier; CLI-friendly curl surface.
- Webhook ingest (GitHub PR/push) with HMAC verify + dedupe; synthetic intents.
- Clustering v0 (path-overlap + text similarity), relations incl. supersede propagation.
- Priority scheduler + Tier 0–2 validation ladder executing via simulator/docker providers.
- Failure taxonomy subset: infra_transient retry, deterministic_regression → bounded repair authorization (agent re-submits; no autonomous repair-agent dispatch yet).
- Event-sourced hash-chained ledger + projections; evidence invalidation on merge-base advance.
- Web UI: change graph, cluster view, dossier, decision explanations.
- Idempotency + backpressure mechanisms as specified in §5.

**OUT (explicitly deferred)**
- github-connector live behavior: check/status write-back, diff reads, merge-queue awareness *(see tension below)*.
- Merge trains / integration sets / autonomous merge authority.
- Canary, deploy, rollback, production outcome loop.
- ML test selection (heuristics only); flake quarantine automation.
- k8s/firecracker providers (interfaces only); multi-host artifact store (S3).
- Multi-tenant hard isolation, SSO/RBAC beyond single admin token, data-residency controls.
- MCP server + SDKs (raw HTTP API only).

**Tension flagged:** the spec's headline demo ("one GitHub Check with a traceable explanation") requires check write-back, which defaults assign to Wave 2. Recommendation: pull a minimal `check-writer` (single static "Agent Verification Gate" posting) into v1 behind the existing service binary — it's ~200 lines against the GitHub Checks API and unlocks the entire demo narrative. Needs sign-off from whoever owns Wave 2 scoping.

---

## Appendix A: challenges to working defaults

1. **Accepted:** monorepo, Go, Next.js, PG16+Redis7, compose-first, simulator/docker-first, 4-service split — all sound at this stage.
2. **Modified:** agent API served *through* ingest but executed synchronously by control-plane (`/commands`) rather than pure async event ingestion — agents cannot proceed without lease answers, and sync commands give us natural idempotency anchors (`command_log`).
3. **Guard-railed:** "control-plane" must not become a god-service. Domain, scheduler, evidence, and lease logic live as separate Go packages with enforced import rules inside `services/control-plane`, each independently extractable post-v1.
4. **Flagged:** github-connector as full Wave 2 conflicts with the v1 demo story (§10 tension). Minimal check-writer should move into v1 scope.

