# Sauron — Edge-Case Matrix (FINAL v1)

Status: FINAL · Derived from `docs/plans/EDGE_CASES_DRAFT.md` (all 64 rows retained). Owner: QA & Verification (W1e).

Every row must end up automated-green, explicitly pending (owned, never a silent skip), or signed-off manual risk before platform v1 is "trustable" (TEST_STRATEGY_DRAFT §4).

## Coverage legend

| Status | Meaning |
|---|---|
| `covered` | Asserted by an automated suite that runs today. Contract mode runs with plain `cd tests && pnpm test`; live/e2e modes arm when `SAURON_API_URL` is set (invariants) or `SAURON_E2E=1` (compose journeys). |
| `pending-W2` | Trigger surface or enforcement lands with W2/W3; expected behavior already encoded where possible (contract predicates, chaos hooks) and becomes a failing live assertion once the surface exists. |
| `manual` | Requires tooling/repo access unavailable in v1 CI; accepted-risk sign-off required. |

Suite paths relative to `tests/`.

## Webhook layer (EC-001 – EC-011)

| ID | Description | Expected correct behavior | Covering suite · test | Sev | Status |
|---|---|---|---|---|---|
| EC-001 | Duplicate delivery GUID sent twice | Exactly-once effect; redelivery acked 2xx, zero extra effects | `invariants/i12.spec.ts` · "signed webhook delivered twice under one GUID produces ONE delivery.accepted"; `e2e/webhook-dedup-replay.spec.ts` · "EC-001: same delivery GUID twice" | S0 | covered |
| EC-002 | Out-of-order events (push after PR-close) | Causal projection; late push creates no work on closed PR | — (projection behavior) | S2 | pending-W2 |
| EC-003 | Invalid HMAC signature | Reject 401, never persist | `e2e/webhook-dedup-replay.spec.ts` · "EC-003: invalid HMAC ⇒ 401" | S0 | covered |
| EC-004 | Validly-signed replay beyond tolerance window | Age-window rejection; dedupe catches exact replays | `invariants/i12.spec.ts` contract (exact-replay identity predicate); age-window needs clock control | S1 | pending-W2 |
| EC-005 | Malformed JSON payload | Quarantined/poison path, service stays healthy | `e2e/webhook-dedup-replay.spec.ts` · "EC-005: malformed JSON ... not fatal" | S2 | covered |
| EC-006 | Huge payload above body-size limit | Streaming cap, 413, bounded memory | — | S2 | pending-W2 |
| EC-007 | Unknown event/action enum value | Persist forward-compat record, ack, alert counter | — | S3 | pending-W2 |
| EC-008 | Missing installation/uninstalled app | Park event, fail-closed tenancy, no leakage | — | S1 | pending-W2 |
| EC-009 | Repo deleted mid-flight while validating | Graceful lease teardown, voided candidates, no zombies | — | S1 | manual |
| EC-010 | Old-secret signature during rotation overlap | Accept old+new inside window, hard-fail outside | — | S2 | pending-W2 |
| EC-011 | N concurrent copies of same delivery | Race-safe dedupe via unique constraint; one effect | contract identity in `invariants/i12.spec.ts`; concurrent leg rides storm (`scenarios/storm.ts`) | S1 | pending-W2 |

## Intent / candidate lifecycle (EC-012 – EC-021)

| ID | Description | Expected correct behavior | Covering suite · test | Sev | Status |
|---|---|---|---|---|---|
| EC-012 | Two candidates same surface simultaneously | Both admitted, classified, tournament, none dropped | `e2e/supersede-propagation.spec.ts` · "duplicate candidates converge: loser superseded..." | S1 | covered |
| EC-013 | Candidate after intent closed/expired | 409 late_submission; zero compute spent | Conflict contract encoded in `invariants/lib/api-schemas.ts` (conflictReasonSchema); close API is W2 | S1 | pending-W2 |
| EC-014 | Supersede arrives while validating | Cancel + bounded drain; late results diagnostics-only | `e2e/supersede-propagation.spec.ts` · "validation.cancelled for loser ... reason superseded" | S1 | covered |
| EC-015 | Cancel during repair loop | Halt at checkpoint, budget reconciled exactly once | — | S1 | pending-W2 |
| EC-016 | Intent deadline expires mid-validation | Watcher cancels remainder; timeout distinct from failure | — | S2 | pending-W2 |
| EC-017 | Relation misclassified as composable; merge breaks | Integration stage NEVER waives composition validation | — | S0 | manual |
| EC-018 | Duplicate intent creation from retried client | Original returned; one graph node | `invariants/i12.spec.ts` · "identical intent creation twice returns identical bodies and one ledger pair"; `scenarios/storm.ts` probe `idempotent_replay_identical` | S2 | covered |
| EC-019 | Identical patch SHA submitted by two agents | Deduplicated lineage, dual attribution | — | S3 | pending-W2 |
| EC-020 | Repair modifies paths OUTSIDE contract (I-05) | SERVER-side pre-accept rejection; incident raised | `invariants/i05.spec.ts` · "path confinement gate rejects every escape shape" (+ glob property tests); live submission API is W2 | S0 | covered (live probe pending-W2) |
| EC-021 | Repair exceeds max_iterations budget | Deterministic envelope close; escalation state | `invariants/i05.spec.ts` · "max_iterations outside 1..5 is rejected" | S1 | covered |


## Scheduler (EC-022 – EC-032)

| ID | Description | Expected correct behavior | Covering suite · test | Sev | Status |
|---|---|---|---|---|---|
| EC-022 | Empty queue tick | Clean no-op, no phantom dispatch, stable health | — (internal tick not black-box observable) | S3 | pending-W2 |
| EC-023 | All budgets exhausted | Queued with reason=budget; 429 signal; auto-resume on release | `invariants/i06.spec.ts` · "concurrent burst yields only successes or typed rejections"; resume leg rides storm | S1 | covered |
| EC-024 | Priority ties (identical scores) | Deterministic tie-break; reproducible storms; no livelock | `invariants/i13.spec.ts` · "tie-break comparator is a strict total order" | S2 | covered |
| EC-025 | Starvation of low-risk work under high-priority load | Aging floor guarantees dispatch bound under random streams | — (bound proof lives in scheduler property tests + storm streams) | S1 | pending-W2 |
| EC-026 | Thundering herd on base-branch advance | Batch invalidation, staggered replan, NO stale reuse (I-02) | `scenarios/storm.ts` · `chaosBaseAdvanceStampede` hook armed (`--chaos`) | S0 | pending-W2 |
| EC-027 | Cancellation AFTER job completed (race) | Terminal job ignores cancel; accepted evidence stands | `invariants/i03.spec.ts` · live "second completion ... already_accepted"; absorption property in `invariants/i08.spec.ts` | S0 | covered |
| EC-028 | Duplicate dispatch attempt (restart replay) | Idempotent by (job, fence); runner sees one lease | — (scheduler restart injection is chaos-tier) | S0 | pending-W2 |
| EC-029 | Scheduler crash mid-dispatch | Atomic re-dispatch same fence or expire; no lost work | — (chaos drillbook, W3) | S0 | pending-W2 |
| EC-030 | Budget released twice (expiry races completion) | Release idempotent; Σdeltas == capacity always | `invariants/i06.spec.ts` · conservation predicates; idempotent release in `e2e/lease-ttl-expiry.spec.ts` | S1 | covered |
| EC-031 | Clock skew inverts deadline/priority order | Ordering from ledger logical time only | `invariants/i13.spec.ts` · "seq is contiguous ... regardless of occurred_at ties" | S2 | covered |
| EC-032 | Merge-base advanced between evidence and merge auth | Decision-time freshness re-check; stale downgraded | — (needs decision-time base move; storm hook W3) | S0 | pending-W2 |

## Evidence (EC-033 – EC-041)

| ID | Description | Expected correct behavior | Covering suite · test | Sev | Status |
|---|---|---|---|---|---|
| EC-033 | Skipped test counted as positive (I-01) | Validator rejects skipped/quarantined for required slots | `invariants/i01.spec.ts` (schema enum + accept-precedence properties + live dossier traceability) | S0 | covered |
| EC-034 | Expired/mismatched evidence reused (I-02) | Decision-time TTL+inputs_hash check; mismatch = miss | `invariants/i02.spec.ts` (digest sensitivity + live base-move hash change) | S0 | covered |
| EC-035 | Two accepted records per lease/run (I-03) | Unique per (run_id,attempt) and per lease jti | `invariants/i03.spec.ts` (uniqueness properties + live fleet-complete-twice → already_accepted) | S0 | covered |
| EC-036 | Evidence for wrong commit SHA vs lease | Provenance lease↔SHA↔digest binding at acceptance | provenance rule mirrored in `invariants/lib/evidence-rules.ts` (`lease_provenance_mismatch`) | S0 | pending-W2 |
| EC-037 | Partial result upload (crash mid-upload) | Manifest atomicity; partials discarded; retry while alive | — | S1 | pending-W2 |
| EC-038 | Evidence hash mismatch / tamper attempt | Quarantine, NEVER accept; chain custody intact | `invariants/i07.spec.ts` · golden+tampered fixtures & tamper property; live tail re-verification | S0 | covered (alerting/suspension W2) |
| EC-039 | Flaky failure counted as deterministic regression | Forensics before classification; known-flake tracked | — | S1 | pending-W2 |
| EC-040 | Concurrent acceptance race (two validators) | Exactly ONE EvidenceAccepted survives | uniqueness gate over arbitrary sequences in `invariants/i03.spec.ts`; DB race probe needs compose load | S1 | pending-W2 |
| EC-041 | Quarantine gaming to hide a failing test | Corroboration across runs + auto-expiry + visible obligation | — | S0 | manual |

## Fleet / runners (EC-042 – EC-049)

| ID | Description | Expected correct behavior | Covering suite · test | Sev | Status |
|---|---|---|---|---|---|
| EC-042 | Runner never returns (silent death) | TTL reap; retry on FRESH fence; ghost completion rejected | terminal-refusal half in `e2e/lease-ttl-expiry.spec.ts` (409 after release/expiry); ghost-rejection via I-11 fence family | S0 | pending-W2 |
| EC-043 | Runner returns malformed result | Safe parse fail; not accepted; runner penalized | — (sim fault modes W3) | S1 | pending-W2 |
| EC-044 | Runner exceeds time budget | Enforcer cancels at deadline; classified timeout ≠ regression | — | S1 | pending-W2 |
| EC-045 | Runner claims same job twice | Second claim 409 demanding fresher fence; first intact | — (claim protocol is internal; probe ready in `invariants/lib/live.ts`) | S1 | pending-W2 |
| EC-046 | Stale fence upload after reassignment (I-11) | Stale fenced writes rejected; current holder authoritative | `invariants/i11.spec.ts` (stale vectors + live wrong-token → fence_mismatch, correct → accepted) | S0 | covered |
| EC-047 | OOM / SIGKILL mid-run | Infra classification; cleanup verified; artifacts discarded | — (chaos drillbook, W3) | S2 | pending-W2 |
| EC-048 | Provider unavailable | Circuit breaker; queued backpressure; NO fabricated results | — | S2 | pending-W2 |
| EC-049 | Credential over-scope attempt (I-04) | Deny at issuance AND runtime scope check; audited | `invariants/i04.spec.ts` (scope/TTL/budget schema bounds + live foreign-fence heartbeat refusal) | S0 | covered |


## Multi-tenancy (EC-050 – EC-055)

| ID | Description | Expected correct behavior | Covering suite · test | Sev | Status |
|---|---|---|---|---|---|
| EC-050 | Cross-tenant reads by ID guessing (I-14) | Uniform 404 indistinguishable from nonexistent | `invariants/i14.spec.ts` · "nonexistent vs malformed vs foreign-shaped ids all return one uniform 404" + tenant-smuggle probe | S0 | covered |
| EC-051 | Per-tenant budget enforcement under contention | Hard isolation; A cannot consume B's reserved capacity | — (dev stack has a single tenant token) | S1 | pending-W2 |
| EC-052 | Noisy neighbor storms thousands of candidates | Per-agent caps; other tenants' SLOs hold | `scenarios/storm.ts` drives the load leg; cross-tenant SLO assertion needs multi-tenant env | S1 | pending-W2 |
| EC-053 | Tenant deleted while candidates running | Graceful teardown; retention honored; no orphan billing | — | S1 | pending-W2 |
| EC-054 | Cross-tenant cache/artifact poisoning | Namespaced keys; cross-tenant fetch denied even for identical bytes | reuse-key namespacing property in `invariants/i02.spec.ts`; fetch-path denial is W2 | S0 | pending-W2 |
| EC-055 | Quota boundary race: last CPU-minute claimed concurrently | Atomic reservation; oversubscription impossible; losers queue cleanly | conservation predicates in `invariants/i06.spec.ts`; boundary race rides storm | S2 | pending-W2 |

## Data / ledger (EC-056 – EC-064)

| ID | Description | Expected correct behavior | Covering suite · test | Sev | Status |
|---|---|---|---|---|---|
| EC-056 | Hash-chain break detection (I-07) | Verifier detects break; projections halt fail-closed; read-only mode available | `invariants/i07.spec.ts` · golden fixtures, per-field tamper property, live paged-tail verification | S0 | covered |
| EC-057 | Projection rebuild diverges from live | rebuild-from-genesis == live for ANY history; mismatch blocks decisions | absorption/idempotence reducer properties in `invariants/i08.spec.ts`; rebuild job itself is W2 | S0 | pending-W2 |
| EC-058 | Clock skew reorders causal sequence | occurred_at vs received_at separate; causal ordering wins | `invariants/i13.spec.ts` · logical seq order independent of wall-clock ties | S2 | covered |
| EC-059 | Forged reuse of existing ULID | Uniqueness rejects conflicting create; logged as tampering | read-path uniformity in `invariants/i14.spec.ts`; write-path collision probe needs internal API exposure | S2 | pending-W2 |
| EC-060 | Migration rollback with live event history | Expand-contract rehearsed against prod-like snapshot; zero loss | — | S1 | manual |
| EC-061 | Old producer events read by newer consumer | Versioned envelopes + upcasters; unknown major parks safely | envelope `version` pinned to 1 in contract mode (`invariants/i07.spec.ts` schema-validity); upcaster pairs are W2 | S2 | pending-W2 |
| EC-062 | At-least-once processing double-applies an effect (I-12) | Every effect idempotent via dedupe table; multiset convergence | `invariants/i12.spec.ts` (command replay) + `invariants/i08.spec.ts` · "multiset converges" | S0 | covered |
| EC-063 | Cancellation processed after MergeAuthorized (post-terminal) | Terminal state machine logs-and-ignores; nothing invalidated | `invariants/i08.spec.ts` · "any events appended after the aggregate goes terminal never mutate state"; live injection endpoint W2 | S0 | pending-W2 |
| EC-064 | Torn/partial event write (crash mid-insert) | Transactional appends only; gap detection triggers rebuild verify | continuity half in `invariants/i07.spec.ts` live tail; crash injection is chaos drillbook | S1 | pending-W2 |

---

## Summary

64 rows · **covered 23** · **pending-W2 37** · **manual 4** (EC-009, EC-017, EC-041, EC-060).
Severity mix unchanged from draft: 24 × S0, 19 × S1, 15 × S2, 6 × S3.
All five core design-doc invariants map to dedicated suites: I-01→EC-033 (`i01`), I-02→EC-034 (`i02`), I-03→EC-035 (`i03`), I-04→EC-049 (`i04`), I-05→EC-020 (`i05`). Ledger integrity I-07→EC-056 (`i07`), idempotency I-12→EC-062 (`i12`), tenancy I-14→EC-050 (`i14`).

**Run modes**

```bash
cd tests
pnpm install
pnpm test              # contract mode: green without any server (live/e2e skip loudly)
SAURON_API_URL=http://localhost:8081 pnpm exec vitest run invariants   # live probes vs compose stack
SAURON_E2E=1 SAURON_API_URL=http://localhost:8081 pnpm exec vitest run # full compose journeys
pnpm exec tsx scenarios/storm.ts --concurrency 500 --repos 8 --dupes 4 [--chaos]
```

Pending rows carry their expected behavior in contract predicates or armed hooks today; they convert to red live assertions as W2/W3 surfaces land. Manual rows require explicit accepted-risk sign-off before platform v1 exit.

