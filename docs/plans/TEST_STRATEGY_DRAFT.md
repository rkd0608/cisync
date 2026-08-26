# CISync — Test Strategy Draft (v1)

Status: DRAFT · Owner: QA & Verification Strategist · Scope: testing the Sauron platform itself.

Product promise = calibrated trust: *"we ran fewer tests without allowing unsafe changes to escape."* Every choice below exists to prove that sentence true — and to detect loudly when it becomes false.

## 1. Test Pyramid, Adapted

Standard pyramids optimize defect-cost-per-dollar. Ours must additionally optimize for **invariant survival under concurrency**: catastrophic Sauron failures are rarely single-function bugs — they are races (cancel-vs-complete, dispatch-vs-crash), trust breaches (skipped test counted as evidence, stale evidence reuse), or statistical failures (test-selection false negatives). Weight shifts up: property-based tests are mandatory, chaos is a first-class tier.

| Level | What lives here | Who writes it | CI gating policy |
|---|---|---|---|
| **U — Unit** | Pure logic: HMAC verification, delivery dedup keys, repair-contract path globs, priority scoring math, evidence-status validation (`skipped ≠ pass`), budget arithmetic, cache-key derivation, fence comparison, event hash chaining. | All engineers + coding agents; human review mandatory for invariant packages (`*/invariants/*`, `*/evidence/*`, `*/sched/*`) | Blocking every PR. Coverage ≥85% global; ≥95% on invariant packages |
| **P — Property-based** | Invariants over arbitrary inputs/orderings: event-application idempotence (any multiset of deliveries converges to one state), budget conservation (Σdeltas == capacity), fence monotonicity, cache-key sensitivity (any output-affecting input change ⇒ new key ⇒ no reuse), deterministic priority tie-breaks, anti-starvation bounds under random arrival streams, hash-chain tamper detection | Platform engineers own generators; agents may add cases but never weaken assertions/shrinking | Blocking on PRs touching owned packages; full sweep nightly; any property failure freezes release |
| **C — Contract** | REST agreements ingest(:8080) ↔ control-plane(:8081) ↔ runner-fleet(:8082), simulated-runner-provider protocol, webhook envelope schemas. Generated from OpenAPI; consumer expectations encode semantics (e.g., duplicate delivery ⇒ 2xx AND zero effects) | Provider owners publish specs; consumers author expectation suites | Blocking every PR; spec-breaking diff fails PR automatically |
| **I — Integration (compose black-box)** | Full docker-compose stack (Postgres ledger, Redis, 3 services, simulated runner). Tests interact ONLY via REST. Covers webhook dedup/order/HMAC/replay, intent→candidate→evidence lifecycle, lease flows, runner fault modes (`never_return`, `malformed_result`, `double_claim`, `stale_fence_upload`, `partial_upload`, `oom_kill`), projection rebuild | Platform team + agents; reviewed by owning service team | "Smoke ring" (<5 min) blocking on PR; full suite blocking on main |
| **S — E2E Storm** | Storm simulator: hundreds of seeded synthetic concurrent candidates — thundering-herd base advances, supersede cascades, budget exhaustion, noisy neighbors. Asserts GLOBAL properties: zero lost work, zero double-dispatch, exact budget conservation, zero evidence-rule violations anywhere in ledger, latency SLOs, starvation bounds | Platform team owns simulator + scenarios; findings feed property suites | Nightly on main + mandatory pre-release; breach blocks release |
| **X — Chaos** | Fault injection into composed system: SIGKILL control-plane mid-dispatch, Redis outage, Postgres restart/partition, runner↔fleet partition, clock-skew injection, redelivered webhooks, cancellation racing completion. Success = recovery + hash-chain verifies + projection rebuild matches + zero invariant violations during window | Platform team; scripted drill runbooks under `tests/scenarios/chaos/` | Weekly + pre-release gate; signed drill report required |

### Cross-cutting rule: invariants executable at every level

Each design-doc invariant is encoded once (shared assertion lib, e.g. `internal/invcheck`) and reused by U/P/I/S/X:

1. A skipped test can never count as positive evidence.
2. If an output-affecting input changes, prior artifact/test results cannot be reused.
3. A job lease may produce at most one accepted evidence record.
4. No runner credential exceeds its declared action / repository / environment / TTL.
5. No agent repair may modify paths outside its granted contract.

If ANY layer's test can construct a ledger state violating these, the build fails regardless of layer ownership.

## 2. Tooling

### Go (primary)

| Concern | Choice | Justification |
|---|---|---|
| Framework | stdlib `testing` (table-driven) + `testify` require/assert | Ubiquitous, excellent failure diffs; deliberately avoid testify suite/mock machinery — prefer hand-written fakes behind port interfaces so async boundaries stay honest |
| Property-based | `pgregory.net/rapid` | Idiomatic Go, fast, automatic shrinking, composable generators — ideal for event-sequence/scheduling-order properties |
| HTTP handlers | `httptest`; contract checks generated from OpenAPI keep code/spec in lockstep | Standard, zero magic |
| DB-backed | Existing compose env preferred (production topology); `testcontainers-go` only where per-test DB isolation is genuinely needed (migration/rebuild tests) | Avoid startup-cost tax everywhere else |

### TypeScript (`apps/`, `packages/`)

| Concern | Choice | Justification |
|---|---|---|
| Unit/component | vitest | Fast, ESM-native; watch loop people actually run |
| Property-based | fast-check | Mirrors Go invariant properties so duplicated policy logic (risk tiers, selection fallbacks) cannot drift between languages |
| Browser E2E | Playwright — DEFERRED until evidence/explain UI is interactive | Don't carry browser-flake tax on a thin UI; component + API contract coverage suffices for v1 |
| Contract sync | Typed clients generated from same OpenAPI specs Go serves | One spec, two languages, zero drift |

### Simulator

Storm simulator = dedicated Go binary reusing provider client libs. Every scenario seed-deterministic (`SCENARIO_SEED`) so failures reproduce bit-for-bit locally. Scenarios as YAML in `tests/scenarios/`. Fault-injecting simulated runner exposes composable failure modes rather than ad-hoc mocks.

## 3. Shadow-Mode Evaluation Harness

Correctness tests prove invariants; shadow mode proves **decision quality** of statistical components (test selection, prioritization, flake classification).

### Architecture
```
replay corpus ─► harness ─► frozen selection engine (policy vX)
(events +       drives      ├─ selected-test plan ─► outcome vs ground truth
 ground truth)  REST APIs   └─ confidence scores ──► calibration analysis
                SHADOW_MODE=1: no external effects;
                simulated runner executes FULL suite = ground truth,
                selected suite = decision input
```

### Corpus sources
1. **Historical replay**: exported event streams paired with full-suite ground truth.
2. **Synthetic labeled streams**: storm output with *planted defects* (mutations injected into known symbols such that specific tests MUST fail) — guarantees detectable ground truth, unlike organic history where "full suite green" may hide misses.
3. **Adversarial sets**: vacuously-passing tests, flake traps, sparse-history repos, renamed files, dependency-bump-only changes.

### Metrics
| Metric | Definition |
|---|---|
| Escaped defects (FN) | Selected tests pass but ground truth finds genuine failure — THE metric; split "caught at merge-train" vs "would have shipped" |
| FN rate by risk tier | Stratified high/med/low; high-risk tier must hit literal zero before autonomy expands |
| FP waste | Tests run whose outcome couldn't change the decision (guards against "run everything") |
| Flake confounding | Escapes re-classified after flake forensics — an escape that is actually a flake changes remedy, not score |
| Calibration | Predicted confidence vs observed escape rate: Brier score + reliability bins; confidence 0.987 must MEAN something |
| Coverage-miss forensics | Per escape: which symbol→test edge was missing; feeds verification graph |
| Savings | Time-to-decision reduction, test-minutes avoided, cache-reuse rate |

### Acceptance thresholds before enforcement autonomy
Promotion along autonomy ladder (L0 observe → L6 deploy); each bar met on frozen corpus with bootstrap-CI lower bounds, then repeated on time-disjoint fresh corpus (no tuning/eval leakage):

| Promotion | Minimum shadow evidence |
|---|---|
| L0→L1 recommendations shown | ≥1,000 replayed candidates; honest savings report; no correctness claim yet |
| L2 trigger pre-approved validation | ≥50 planted-defect candidates, ZERO missed; calibration MAE ≤0.03 |
| L3 bounded repair | Repair-path invariant green at all layers; zero out-of-contract patches in replay |
| L4 merge-eligible marking | 0 unexplained escapes across trailing 3 corpus releases; FN=0 on high-risk planted set; uncertainty>ε fallback widens validation in 100% of sampled cases |
| L5 auto-merge low risk | Above sustained ≥30 days live shadow; policy-owner sign-off; runtime kill-switch tested |

Fallback rules themselves are tested as properties: uncertain prediction, sparse history, or auth/payments/schema/infra touches MUST auto-widen validation ("no candidate below confidence θ receives reduced evidence"). Corpus versions immutable; regression = any metric worse than previous signed release report. Every production escape becomes a corpus fixture (escape → fixture → regression test).

## 4. Definition of Done — platform v1 "trustable"

ALL must hold:

1. **Invariants executable & green** — five core invariants enforced at unit/property/integration/storm levels; violation = release blocker, no waivers.
2. **Edge-case matrix retired** — every row of EDGE_CASES_DRAFT.md automated-green or explicitly signed-off as accepted risk; no unowned S0/S1 rows.
3. **Contracts green** — all three services plus runner-provider protocol; zero breaking drift.
4. **Integration suite green** including every fault-injection runner behavior.
5. **Storm SLOs met**: multi-hour soak, hundreds of concurrent candidates, zero lost/duplicated effects, budgets exactly conserved, p99 dispatch within target, seed-reproducibility demonstrated on at least one recovered failure.
6. **Chaos drillbook executed**: kill -9 mid-dispatch, Redis outage, Postgres restart, network partition — recovery proven via chain verification + projection rebuild equivalence.
7. **Shadow acceptance met** on corpus v1 with signed report; false-negative thresholds satisfied.
8. **Security posture verified**: credential-scoping, tamper-detection, IDOR suites green.
9. **Explainability**: every accept/reject/cancel decision retrievable via API with policy version + inputs + rationale; audit-log completeness property holds.
10. **Kill switch**: every autonomy level disableable at runtime without redeploy — tested, not assumed.
