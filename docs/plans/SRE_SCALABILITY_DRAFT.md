# SRE Scalability & Reliability Plan — DRAFT v1

Owner: Staff Systems Engineer (Scalability & Reliability) · Status: PLANNING, no code
Scope: control-plane reliability under 100x-normal agent arrival rates. Companion to the product design doc (`CI_CD and build systems Most pipelines were design.md`).

---

## 1. Capacity model (back-of-envelope)

### 1.1 Target workload

| Metric | Value | Derived |
|---|---|---|
| Active intents | 500 steady-state | ~4 candidates each → ~2,000 live candidates |
| New candidates/day | 2,000 | 0.023/s avg; 10x diurnal peak ≈ 0.23/s |
| Validations/day | 50,000 | 0.58/s avg; peak 5x ≈ 3/s |
| Avg validation duration | 6 min (tier-1/2 mix) | Little's law: **~210 concurrent validations** steady |
| Storm burst (design point) | **30 validations/s arrivals sustained 10 min** | ⇒ 10,800 *demanded* concurrency vs. fleet cap of ~1,000 runners. **Arrivals exceeding capacity is the normal case, not the exception** — shedding speculation IS the product. |
| Webhook burst | 100/s for 60s, avg 8 KB payload | 800 KB/s ingress |

### 1.2 Per-component estimates

| Component | Load at burst | Resource estimate | Headroom notes |
|---|---|---|---|
| Ingest (HMAC + dedup + append) | 100 wh/s × 2.5 events emitted = 250 events/s | 2 vCPU / 2 GB; 50 goroutines | HMAC-SHA256 @ ~1 GB/s/core → <0.1 ms/event. **Never the bottleneck.** |
| Postgres 16 event store | 250 events/s burst ≈ 0.9 GB/h WAL | 8 vCPU / 32 GB NVMe; daily partitions | Batched COPY handles 10k+/s → ~40x headroom. Long-term risk = index bloat/vacuum, not insert rate. |
| Control plane (scheduler + state machines) | Score 2k candidates × ~25 features/tick ≈ trivial CPU; cost is PG round-trips + dispatch writes | 4 vCPU / 8 GB; 16 shards keyed by repo-hash | Tick budget 1 s, p99 target <500 ms. Hot-row contention on popular-repo intent aggregates is the real ceiling (see §2). |
| Runner-fleet gateway | Lease ops ~210 renewals/min per 250 active jobs; heartbeats trivially small | 2 vCPU / 4 GB per 500 runners | Startup dominates: cached image 5 s, cold pull 45–90 s. Warm pool only for top-5 images + tier-0/1 classes. |
| Environment leases | Scarce by design: cap 200 concurrent preview envs | n/a — policy knob | Tier-3+ latency will be dominated by lease wait; that's intended backpressure. |
| Redis 7 (if kept — see §9) | Dedup SETNX 100/s; rate-limit tokens ~500 ops/s | 1 vCPU / 2 GB | Trivial load; kept only as a failure domain worth deleting. |

### 1.3 Bottleneck order-of-failure (be specific)

1. **Runner/env capacity** (Little's law above) — first and always. Mitigation: admission control §3.
2. **PG hot-row contention**: a supersede storm on one repo serializes optimistic-concurrency retries on its intent aggregate. Mitigation: shard by repo, batch supersede events, retry with jitter; alert if conflict-rate >2%/tick.
3. **Outbox dispatch fanout** during webhook bursts (dispatcher poll lag). Mitigation: partitioned outbox, parallel dispatch per shard.
4. **Projection catch-up after PG failover** — replay backlog of minutes. Mitigation: idempotent rebuild, §4/§5.
5. **Not bottlenecks**: webhook HMAC verify, Redis ops, scheduler arithmetic.

---

## 2. Concurrency correctness patterns (technique → owner → test)

| Pattern | Where / owner | Mechanism | How it's tested |
|---|---|---|---|
| **Idempotency keys** | Ingest owns `webhook_delivery_id` | Redis `SET NX EX` fast-path + unique index in PG as source of truth; duplicate → return stored result (200, no-op) | Unit: replay same delivery twice → exactly one event row. Chaos: double-delivery storm test in §7 asserts zero duplicate events. |
| **Transactional outbox** | Control plane owns write path | State change + outbox row in **same PG tx**; separate dispatcher goroutine polls, marks sent, deletes after ack | Integration: kill dispatcher mid-batch (SIGKILL between tx commit and mark-sent) → redelivery; consumer dedup makes effect once. Assert outbox never has unsent rows older than N s. |
| **Optimistic concurrency on aggregates** | Control plane: `intent`, `candidate`, `validation` tables each carry `version INT` | `UPDATE ... WHERE version=$n`; conflict → re-read, re-evaluate, retry ≤3 with jitter; scheduler shards by repo so cross-shard conflicts don't exist | Property-based: N writers race one aggregate → final version monotonic, zero lost updates. Inject 10% artificial conflicts; assert convergence. |
| **Fencing tokens (scheduler→runner dispatch)** | Scheduler mints from a PG sequence per (validation_id) epoch; runner must echo token on every evidence/result call; runner-fleet rejects `token < current` | Prevents zombie runner writes after lease loss/retry-dispatch | Fault-injection: revoke lease, force redispatch to new runner, old runner submits late → assert old evidence rejected + `fenced_writes_total > 0` metric fires. |
| **Lease TTLs + renewal** | Runner-fleet owns job leases (TTL 120 s, heartbeat every 40 s); env leases TTL 25 min default | Lease = row in PG (`expires_at`) + Redis mirror for O(1) checks; renewal extends atomically via compare-and-set on `lease_epoch` | Simulated clock tests: stop heartbeats → expiry detected within TTL+grace (≤160 s); renewal racing expiry never yields two live holders (assert single winner over 1k races). |
| **Dead-man switch** | Reconciler (§5) | If scheduler instance dies, its `scheduler_lease` expires → another replica takes over with higher fence epoch | Kill active scheduler mid-tick; standby picks up <15 s; assert no duplicate dispatches post-takeover (fence check). |
| **Exactly-once effects despite at-least-once delivery** | Every state-mutating consumer | Effects applied inside tx that inserts into `processed_events(event_id PK)`; duplicate event_id → tx no-ops. Evidence acceptance additionally keyed `(validation_id, attempt)` — matches invariant "a job lease may produce at most one accepted evidence record" | Replay entire day's event log twice against a forked DB → byte-identical projections (deterministic replay test, runs nightly in CI). |

---

## 3. Backpressure & admission control

### 3.1 Layer-by-layer behavior when arrivals exceed capacity

| Layer | Signal | Action |
|---|---|---|
| **Ingest** | Queue depth >2k events or append latency p99 >200 ms | Return 429 + `Retry-After` to GitHub (it redelivers); keep appending what fits. Never drop silently. |
| **Event store** | WAL/disk headroom, replication lag >30 s | Throttle projection consumers before ingest; ingest is protected (source of truth). |
| **Scheduler** | Ready-queue depth >5k validations or tick p99 >1 s | Stop promoting speculative tiers; merge-train work bypasses. Queue bounded at 20k → oldest tier-1 items demoted to "parked" (persisted, resumable), never lost. |
| **Fleet** | Concurrent jobs ≥ cap OR warm-pool drain rate > refill | Admission tokens per work class; excess waits as parked demand (re-scored each tick — a parked item may be cancelled by supersede instead of running: wasted-compute avoidance is the point). |

### 3.2 Shed-vs-buffer-vs-reject matrix

| Work class | Buffer? | Shed order | Reject? |
|---|---|---|---|
| Tier 4 merge-train / incident hotfix | Unlimited (dedicated queue) | Never shed | Never; preempts others instead |
| Tier 3 system/E2E | Bounded 1k | After tier-2, before tier-4 needs | Only if env-lease wait > deadline − est. runtime |
| Tier 2 contract/integration | Bounded 5k | 2nd | No — park |
| Tier 1 unit/static/speculative | Bounded 10k | **First** (and auto-cancelled on supersede) | Yes if tenant budget exhausted → 429 to agent with reason |

### 3.3 Budget enforcement mechanism

Per-tenant and per-intent token buckets, checked **atomically at admission** (single Lua script if Redis kept; single `UPDATE ... WHERE budget_remaining >= cost RETURNING` row if PG-only): reserves estimated cost before dispatch, refunds unused on completion/cancellation. Enforcement accuracy is an SLI (§6): ≥99% of actual spend must fall within declared budget; violations page. Agents receive machine-readable rejection: `{reason: budget_exhausted, retry_after, scope}` — cheap feedback loops prevent agents from hammering.

### 3.4 Queue depth metrics (all layers)

`sorn_queue_depth{layer,work_class,tenant}`, `sorn_parked_validations`, `sorn_admission_rejections_total{reason}` — these three are the primary "queue explosion" tripwires.

---

## 4. Failure modes & recovery (v1 RPO/RTO posture)

| # | Failure | Detection | Automatic recovery | Data-loss posture (v1) |
|---|---|---|---|---|
| 1 | Process crash mid-event-write | Supervisor restart; PG tx atomicity | Tx rolls back whole; client/GitHub redelivers; idempotency dedups | **RPO 0** for event store (single-tx atomicity) |
| 2 | PG failover (primary loss) | Health probe fails 3×5s | Promote sync replica; clients reconnect; scheduler re-acquires leader lease with bumped fence epoch; replays unacked outbox | RPO 0 w/ synchronous replica (accept ~2x write latency); RTO ≤60 s auto. Projections may lag minutes → reconciler catch-up (§5). |
| 3 | Redis total loss | Conn errors + `redis_up=0` | **Design goal: safe to lose entirely.** All leases/dedup/rate-limits have authoritative PG state; on loss, treat ALL leases as expired → reconciler kills+fences in-flight jobs conservatively (wasted compute, not corruption); rebuild Redis from PG | RPO: full loss acceptable **by construction**; RTO: rebuild ≤60 s. (This posture is why I propose dropping Redis — §9.) |
| 4 | Runner dies silently | Lease not renewed by TTL+grace (≤160 s) | Reconciler marks validation failed-infra, fences old token, redispatches (attempt++ ≤3, different zone preferred) | No evidence loss (none accepted); wasted partial compute bounded by TTL |
| 5 | Duplicate webhook delivery | Idempotency key hit | No-op 200 | Zero duplication by invariant |
| 6 | Clock skew between nodes | NTP drift metric >500 ms alarms | Logic uses PG `now()` / monotonic intervals for ordering; fencing compares token values, **never wall clocks across nodes** | Skew degrades lease-expiry precision only; fenced writes remain impossible |
| 7 | Network partition control-plane↔runner-fleet | Heartbeat gaps; gateway-side local queue | Runners continue + buffer results locally up to 1 h; on heal, bulk-submit with tokens (fencing rejects stale) | Buffered results survive ≤1 h partition; beyond that, jobs killed+fenced on heal |

---

## 5. Reconciliation loops (periodic reconciler)

Single Go worker, scan cycle every **30 s**, shard-scannable, fully idempotent, emits `repair_action` audit events:

| Scan | Invariant enforced | Repair |
|---|---|---|
| Running validations with expired leases | Every running job holds a valid lease | **Kill+fence**: cancel job, bump fence epoch, classify infra-failure, redispatch if attempts remain |
| Leased jobs whose runner stopped heartbeating but process alive elsewhere | One live holder per lease | Fence loser (token check makes this automatic); kill orphaned process |
| Events recorded but projection stale (`event.version > projection.version` or outbox age >60 s) | Projections = deterministic fold of log | Rebuild affected aggregates from event log; alert if rebuild rate sustained |
| Env leases expired but resources still provisioned | Lease table ↔ actual envs converge | De-provision; reclaim budget; record leak metric |
| Intents non-terminal past deadline, candidates superseded but still dispatched, validations with no terminal event >2×est-runtime | No zombie work | Cancel+fence; notify owning agent with reason |
| Evidence accepted without matching completed validation; accepted count ≠1 per lease epoch | "One accepted evidence record per job lease" | Quarantine evidence, page (invariant violation = bug, not noise) |
| Budget ledgers vs. sum(reserved+spent) | Ledger additive closure | Recompute from event log; discrepancy >0.5% pages |

Reconciler health itself: `sorn_repairs_total{type}`, `sorn_reconciler_lag_seconds` (scan-cycle duration vs. period).

---

## 6. Observability plan

### 6.1 Golden signals per service (Prometheus `/metrics`, v1-minimal stack; Grafana later)

| Service | Latency | Traffic | Errors | Saturation |
|---|---|---|---|---|
| Ingest | verify+append p99 | webhooks/s, events/s | HMAC fails, dupes, 429s | inbound queue depth |
| Control plane | tick duration p99/p999; decision latency | decisions/s, dispatches/s | OCC conflicts, fence rejections | ready-queue depth, parked count |
| Runner fleet | dispatch→running p95; heartbeat lag | jobs/s by class | silent deaths, fenced writes | active/cap ratio, warm-pool level |
| Reconciler | scan-cycle duration | repairs/min by type | invariant violations | lag behind period |

### 6.2 Logging conventions
JSON lines. Mandatory fields: `ts, level, msg, service, trace_id, span_id, correlation_id, causation_id, tenant_id, intent_id?, candidate_id?, validation_id?, fence_token?`. Correlation/causation IDs propagate from the event envelope into every log/metric/span. **Redaction at emit**: GitHub tokens, agent credentials, secret-scan hits, file contents >2-line context, customer code paths beyond top-level dir. Redaction failures are log-and-drop, never pass-through.

### 6.3 Key SLIs/SLOs (v1)

| SLI | SLO |
|---|---|
| Webhook→decision latency (admitted work, excl. deliberate queueing) | p50 <5 s, p95 <30 s |
| Scheduler tick latency | p99 <500 ms (budget 1 s) |
| Evidence staleness (decision time − newest evidence timestamp, normalized by validation class) | p95 <15 min |
| Budget-enforcement accuracy (spend within declared budget) | ≥99%; any breach pages |
| Lease-expiry→cancel propagation | ≤160 s p99 |
| Event-store durability | RPO 0 measured (no committed-tx loss drills quarterly) |

---

## 7. Load & chaos testing plan

### 7.1 Storm simulation ("400-agent morning")
Shape: 24 h compressed to 2 h; ramps — baseline 3 wh/s → step to 100 wh/s ×60 s (GitHub retry pattern) → sustained 25 wh/s ×30 min simulating 400 concurrent agents opening overlapping intents (30% semantic duplicates, 10% supersede storms on 3 hot repos) → tail 30 val/s arrivals. Runs weekly in CI-nightly against docker-compose profile `storm`.

Assertions (hard fail):
- Zero lost decisions for admitted tier-3/4 work; zero duplicate evidence records.
- webhook→decision p95 <30 s at 25 wh/s steady; tick p99 <500 ms throughout burst.
- Queue depths recover to baseline within 5 min of ramp-down; parked items all terminal within 15 min.
- Supersede cancels ≥90% of doomed speculative work before dispatch (wasted-compute KPI).
- Budget ledger closure error <0.5%.

### 7.2 Automated fault injections (each = one CI scenario with assertion)
1. SIGKILL control-plane mid-outbox-dispatch → no event loss, no dup effect.
2. PG primary kill during storm → failover <60 s, RPO 0 verified by checksum.
3. `FLUSHALL` Redis under load → all leases expire-safe, zero fenced-write escapes, rebuild <60 s.
4. Toxiproxy 5-min partition CP↔fleet → buffered results flush correctly post-heal.
5. Revoke 30% runners mid-job → redispatch ≤3×TTL, attempt caps respected.
6. Duplicate-delivery storm (every webhook 3×) → zero duplicate events.
7. Clock jump +10 min on one node → no incorrect cancellations (PG-time logic).
8. Reconciler kill for 10 min then release → backlog cleared <2 cycles, no thrash.

Success criteria gate: all scenarios green 3 consecutive nightly runs before any scheduling-semantics change ships.

---

## 8. Runbook skeleton (top 8 alerts)

Format: **Alert → Diagnose → Mitigate**

1. **IngestQueueDepthHigh (>2k, 5m)** → Check GitHub delivery headers + upstream incident; verify PG append p99 → If ingest bug: toggle circuit-breaker returning 429 (GitHub redelivers); scale ingest replicas.
2. **OutboxDispatchBacklog (>60s age)** → Dispatcher liveness? PG lock waits? → Restart dispatcher (idempotent), then check §1.3 bottleneck #3 fan-out sharding.
3. **SchedulerTickP99Breach (>1s, 3 ticks)** → Which shard? Hot repo? OCC conflict rate? → Drain hot shard to standby; enable supersede batching for that repo; page if conflict rate >2%.
4. **LeaseExpirySpike (>5 expiries/min unexplained)** → Fleet-wide agent crash? Network partition? → Check gateway dashboards; freeze redispatch if systemic (avoid thundering herd), invoke §7.2-5 recovery manually.
5. **PGReplicationLag (>30s)** → Replica IO? Vacuum storm? Big replay? → Throttle projection consumers; investigate long tx; never throttle ingest.
6. **EnvLeaseExhausted (wait p95 >deadline−runtime)** → Leak (expired-but-running) or true demand? → Run reconciler env sweep; if true demand, raise cap or shed tier-3 per §3.2 matrix.
7. **BudgetViolationDetected** → Which tenant/intent; admission race? → Halt that tenant's admissions (kill switch), reconcile ledger from log, file correctness bug (P1).
8. **ReconcilerRepairRateSpike (>50/min)** → Upstream mass failure (Redis loss, fleet crash)? Invariant violation? → Identify dominant repair type; if `evidence_invariant` type: quarantine pipeline, page staff eng immediately.

---

## 9. Technology challenges (against working defaults)

1. **Drop Redis for v1.** At our scale (≤1k lease ops/s incl. bursts) Postgres 16 handles leases (expiry column + partial index on `expires_at < now()`), dedup (unique index), and token-bucket budgets (single-statement CAS) comfortably. Rationale: §4.3 shows Redis loss is our nastiest failure mode precisely because it's a second state store; making PG the sole authority deletes an entire failure domain, one deployment dependency, and one consistency model from v1. Revisit at >5k ops/s sustained or p99 lease-check latency >5 ms.
2. **Adopt NATS JetStream now, not later** — as the *delivery bus*, with the PG transactional outbox remaining source of truth (dispatcher publishes to JetStream instead of direct consumer polling). Reasoning: we need durable multi-consumer fanout (projections, audit, notifications, future ML sidecar) with at-least-once + consumer offsets; building that on PG polling per-consumer replicates JetStream poorly. NATS is also a single static Go binary — preserves the single-binary ethos, runs fine in docker-compose, K8s-native later. Cost: one more moving part; mitigated by consumers being idempotent by design anyway (§2).
3. **Keep Go 1.23 / single binary per service — agreed**, with one amendment: shared `internal/` library for envelope, idempotency, fencing, and lease primitives so correctness patterns can't drift per service.
4. **PG partitioning is mandatory day one** (daily partitions on event store + outbox), not a retrofit — vacuum stall on the event table is the slow-burn outage nobody budgets for.

---

## 10. Open questions

- **Architect**: Is JetStream-as-bus acceptable given outbox remains truth? Shard count/fn for scheduler — stable hash vs. consistent hashing as repos churn?
- **Security**: Who custodies evidence-signing keys (per-tenant HSM vs. platform KMS)? Fencing-token exposure in logs — acceptable or truncate?
- **Security/domain**: Do cross-*tenant* result reuse (identical dependency subtrees) ever become legally permissible, or hard isolation forever?
- **Domain planners**: What is the contractual semantics of "parked" work (§3.2) — does a parked candidate owe the agent a decision eventually, and within what SLA?
- **Architect**: GitHub API secondary rate limits — at 100 wh/s burst, our outbound calls (checks/statuses) may be the true external ceiling; do we need outbound shaping per installation?
- **Domain**: Is killing+fencing a still-running job on lease expiry (§5) acceptable product behavior for expensive tier-3 suites, or do those need longer/dead-man-switched grace?
