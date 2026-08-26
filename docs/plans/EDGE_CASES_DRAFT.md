# CISync — Edge-Case Matrix Draft (v1)

Status: DRAFT · Companion to `TEST_STRATEGY_DRAFT.md`. Every row must end up either automated-green or explicitly signed off as accepted risk before platform v1 is "trustable".

**Severity legend**
- **S0** — trust/security breach, invariant violation, or possible wrong merge/deploy decision.
- **S1** — incorrect behavior, lost/duplicated work, liveness risk.
- **S2** — degraded operation, recoverable inconsistency, observability gap.
- **S3** — minor / cosmetic / telemetry-only.

**Coverage paths** are relative to `tests/` (`integration/`, `scenarios/`, `invariants/`). `[AUTOMATABLE-v1]` = coverable by automated suite in v1 stack; `[MANUAL/LATER]` = requires tooling/repo access not yet available.

## Webhook layer (EC-001 – EC-011)

| ID | Description | Expected correct behavior | Coverage suggestion | Sev | Flag |
|---|---|---|---|---|---|
| EC-001 | Duplicate delivery, identical GitHub delivery GUID sent twice | Exactly-once effect: idempotent append keyed on delivery ID; redelivery acked 2xx but applies zero ledger effects | `integration/webhook_dedup_test.go` | S0 | [AUTOMATABLE-v1] |
| EC-002 | Out-of-order events (push arrives after its PR-close) | Store occurred_at + received_at + per-source sequence; projections apply causally; late push creates no new work on closed PR | `integration/webhook_ordering_test.go` | S2 | [AUTOMATABLE-v1] |
| EC-003 | Invalid HMAC signature | Reject 401, never persist, rate-limit source, emit security audit event | `integration/webhook_hmac_test.go` | S0 | [AUTOMATABLE-v1] |
| EC-004 | Validly-signed but replayed old payload beyond tolerance window | Replay-window check on delivery age/timestamp rejects; dedupe also catches exact replays | `integration/webhook_replay_test.go` | S1 | [AUTOMATABLE-v1] |
| EC-005 | Malformed JSON / schema-violating payload | Quarantined to poison store + metric; ingest loop never crashes; inspectable, not silently dropped | `integration/webhook_malformed_test.go` | S2 | [AUTOMATABLE-v1] |
| EC-006 | Huge payload above body-size limit | Streaming size cap, 413, bounded memory, service remains healthy under repeated attempts | `integration/webhook_payload_size_test.go` | S2 | [AUTOMATABLE-v1] |
| EC-007 | Unknown event type / action enum value | Persist `UnknownEventReceived` (ledger forward-compat), ack 200, counter alerts | `integration/webhook_unknown_event_test.go` | S3 | [AUTOMATABLE-v1] |
| EC-008 | Missing installation (event for uninstalled app/repo) | Event parked, not dropped; tenant resolution fails closed; no cross-tenant leakage | `integration/webhook_missing_installation_test.go` | S1 | [AUTOMATABLE-v1] |
| EC-009 | Repo deleted mid-flight while validation running | Reconciliation detects deletion, cancels leases gracefully, voids candidates, no orphan runners or zombie checks | `scenarios/webhook_repo_deleted_test.go` | S1 | [MANUAL/LATER] |
| EC-010 | Signature made with old secret during secret-rotation overlap | Accept old+new secrets within rotation window; hard-fail outside it | `integration/webhook_secret_rotation_test.go` | S2 | [AUTOMATABLE-v1] |
| EC-011 | Retry stampede: N copies of same delivery arriving concurrently | Dedupe is race-safe (unique constraint); exactly one effect despite parallel arrival | `integration/webhook_concurrent_dedupe_test.go` | S1 | [AUTOMATABLE-v1] |

## Intent / candidate lifecycle (EC-012 – EC-021)

| ID | Description | Expected correct behavior | Coverage suggestion | Sev | Flag |
|---|---|---|---|---|---|
| EC-012 | Two agents submit candidates for same surface simultaneously | Both admitted; relation classified (alternative/conflict/composable); bounded tournament; neither silently dropped | `integration/intent_concurrent_candidates_test.go` | S1 | [AUTOMATABLE-v1] |
| EC-013 | Candidate submitted after intent closed/expired | 409 rejection; recorded as LateSubmission for audit; zero validation compute spent | `integration/intent_closed_submission_test.go` | S1 | [AUTOMATABLE-v1] |
| EC-014 | Supersede arrives while validation running | Cancel signal with bounded drain; late results retained as diagnostics ONLY, never accepted as evidence | `integration/intent_supersede_race_test.go` | S1 | [AUTOMATABLE-v1] |
| EC-015 | Cancel arrives during repair loop | Repair halts at iteration checkpoint; partial patch discarded per policy; repair budget reconciled exactly once | `integration/intent_cancel_during_repair_test.go` | S1 | [AUTOMATABLE-v1] |
| EC-016 | Intent deadline expires mid-validation | Deadline watcher cancels remaining jobs; already-accepted evidence still valid; outcome recorded as timeout, distinct from failure | `integration/intent_deadline_expiry_test.go` | S2 | [AUTOMATABLE-v1] |
| EC-017 | Conflicting relation misclassified as composable; merged integration breaks | Relation label NEVER waives composition validation — integration stage catches break; blast radius confined to train; misclassification logged into classifier feedback loop | `scenarios/intent_relation_misclassification_test.go` | S0 | [MANUAL/LATER] |
| EC-018 | Duplicate intent creation from retried client (same idempotency key) | Original intent returned; exactly one graph node created | `integration/intent_idempotent_create_test.go` | S2 | [AUTOMATABLE-v1] |
| EC-019 | Identical patch SHA submitted independently by two agents | Deduplicated to shared evidence lineage; attribution preserved for both submitters | `integration/intent_identical_patch_test.go` | S3 | [AUTOMATABLE-v1] |
| EC-020 | Repair agent modifies paths OUTSIDE granted contract (invariant #5) | Patch rejected at submission API AND caught by post-check; repair iteration terminated; incident raised; contract enforced server-side, not by agent honor system | `invariants/intent_repair_path_violation_test.go` | S0 | [AUTOMATABLE-v1] |
| EC-021 | Repair exceeds max_iterations budget | Repair envelope closes deterministically; escalation state set; unbounded loops impossible | `integration/intent_repair_budget_exhausted_test.go` | S1 | [AUTOMATABLE-v1] |

## Scheduler (EC-022 – EC-032)

| ID | Description | Expected correct behavior | Coverage suggestion | Sev | Flag |
|---|---|---|---|---|---|
| EC-022 | Empty queue tick | Clean no-op, no phantom dispatch, health metrics stable | `integration/sched_empty_queue_test.go` | S3 | [AUTOMATABLE-v1] |
| EC-023 | All budgets exhausted | Work stays queued with reason=budget; backpressure signaled upstream; resumes automatically on release; nothing dropped silently | `integration/sched_budgets_exhausted_test.go` | S1 | [AUTOMATABLE-v1] |
| EC-024 | Priority ties (identical scores) | Deterministic tie-break (age, then ID); reproducible storm runs; livelock impossible | `invariants/sched_priority_tie_test.go` | S2 | [AUTOMATABLE-v1] |
| EC-025 | Starvation of low-risk work under continuous high-priority arrivals | Aging floor guarantees eventual dispatch within stated bound; property test over randomized arrival streams proves bound | `invariants/sched_starvation_test.go` | S1 | [AUTOMATABLE-v1] |
| EC-026 | Thundering herd on base-branch advance (hundreds of candidates stale at once) | Batch invalidation + staggered revalidation plan; priorities recomputed without dispatch stampede; NO stale evidence reused afterward (invariant #2) | `scenarios/sched_base_advance_herd_test.go` | S0 | [AUTOMATABLE-v1] |
| EC-027 | Cancellation arrives AFTER job completed successfully (race) | Terminal job ignores cancel; accepted evidence stands; cancel must NOT kill a different job that reused the same slot/lease identity | `invariants/sched_cancel_after_complete_test.go` | S0 | [AUTOMATABLE-v1] |
| EC-028 | Duplicate dispatch attempt (scheduler restart replays dispatch event) | Dispatch idempotent by (job ID, fence token); runner observes exactly one lease | `integration/sched_duplicate_dispatch_test.go` | S0 | [AUTOMATABLE-v1] |
| EC-029 | Scheduler crash mid-dispatch (job enqueued, ack not persisted) | Recovery reconciliation: job atomically re-dispatched under SAME fence or expired; no lost work, no zombie dual-lease | `scenarios/sched_crash_mid_dispatch_test.go` | S0 | [AUTOMATABLE-v1] |
| EC-030 | Budget released twice (lease expiry races explicit completion) | Release idempotent; budget conservation property holds: Σdeltas == capacity always | `invariants/sched_budget_double_release_test.go` | S1 | [AUTOMATABLE-v1] |
| EC-031 | Clock skew between scheduler nodes inverts deadline/priority order | Ordering decisions derive from ledger logical time; wall-clock inputs advisory only | `invariants/sched_clock_skew_test.go` | S2 | [AUTOMATABLE-v1] |
| EC-032 | Merge-base advanced BETWEEN evidence acceptance and merge authorization | Decision-time freshness re-check; stale evidence downgraded; only invalidated subset rerun; merge blocked meanwhile | `integration/sched_merge_base_freshness_test.go` | S0 | [AUTOMATABLE-v1] |

## Evidence (EC-033 – EC-041)

| ID | Description | Expected correct behavior | Coverage suggestion | Sev | Flag |
|---|---|---|---|---|---|
| EC-033 | Skipped test counted as positive evidence (MUST be impossible; invariant #1) | Validator rejects skipped/quarantined/filtered statuses for required-evidence slots; deferred tests recorded explicitly as non-evidence; property test over arbitrary result statuses proves no path marks skip as pass | `invariants/evidence_skip_never_positive_test.go` | S0 | [AUTOMATABLE-v1] |
| EC-034 | Expired evidence reused past TTL / after input change (MUST be impossible; invariant #2) | Validity evaluated at DECISION time against TTL + full inputs-hash (base SHA, lockfiles, flags); expired/mismatched = cache miss, never hit | `invariants/evidence_expiry_reuse_test.go` | S0 | [AUTOMATABLE-v1] |
| EC-035 | Same job lease producing TWO accepted evidence records (MUST be impossible; invariant #3) | DB unique constraint on accepted-per-lease + state machine; second submission rejected as fenced duplicate even across restarts | `invariants/evidence_lease_single_acceptance_test.go` | S0 | [AUTOMATABLE-v1] |
| EC-036 | Evidence submitted for wrong commit SHA (mismatch vs lease) | Provenance binding lease↔SHA↔artifact digest verified at acceptance; mismatch rejected, lease flagged | `integration/evidence_sha_mismatch_test.go` | S0 | [AUTOMATABLE-v1] |
| EC-037 | Partial result upload (truncated report / crash mid-upload) | Manifest-based atomic acceptance; partial uploads discarded; retry permitted only while lease alive | `integration/evidence_partial_upload_test.go` | S1 | [AUTOMATABLE-v1] |
| EC-038 | Evidence hash mismatch / tamper attempt | Verification fails ⇒ quarantined, NEVER accepted; security alert; hash-chain custody intact; submitting runner suspended pending review | `integration/evidence_tamper_test.go` | S0 | [AUTOMATABLE-v1] |
| EC-039 | Flaky failure counted as deterministic regression | Flake forensics (controlled reruns, environment fingerprints) required before classification; known-flake excluded from gate but tracked as quality obligation; new-flake triggers controlled repeat | `integration/evidence_flake_classification_test.go` | S1 | [AUTOMATABLE-v1] |
| EC-040 | Concurrent acceptance race (two validators evaluate same evidence simultaneously) | Optimistic-concurrency single writer; exactly ONE EvidenceAccepted event survives | `invariants/evidence_concurrent_accept_race_test.go` | S1 | [AUTOMATABLE-v1] |
| EC-041 | Quarantine gaming: agent engineers signals to quarantine a genuinely failing test | Quarantine requires corroboration across independent runs/environments + auto-expiry + human-visible obligation; single actor can never suppress a failing test from the gate | `scenarios/evidence_quarantine_abuse_test.go` | S0 | [MANUAL/LATER] |

## Fleet / runners (EC-042 – EC-049)

| ID | Description | Expected correct behavior | Coverage suggestion | Sev | Flag |
|---|---|---|---|---|---|
| EC-042 | Runner never returns (silent death) | Lease TTL expiry reaps job; classified infra-timeout; bounded retry on FRESH fence; ghost completion arriving later is rejected | `integration/fleet_runner_silent_death_test.go` | S0 | [AUTOMATABLE-v1] |
| EC-043 | Runner returns malformed result | Parse fails safely; evidence NOT accepted; diagnostics preserved; runner penalized/suspended after threshold | `integration/fleet_malformed_result_test.go` | S1 | [AUTOMATABLE-v1] |
| EC-044 | Runner exceeds time budget | Budget enforcer cancels at deadline; classified timeout ≠ product regression; cost fully accounted | `integration/fleet_time_budget_exceeded_test.go` | S1 | [AUTOMATABLE-v1] |
| EC-045 | Runner claims the same job twice | Second claim rejected 409 demanding fresher fence token; first lease unaffected | `integration/fleet_double_claim_test.go` | S1 | [AUTOMATABLE-v1] |
| EC-046 | Fence-token mismatch: superseded worker uploads results after reassignment | Stale fenced writes rejected; only current fence holder's result authoritative | `invariants/fleet_stale_fence_upload_test.go` | S0 | [AUTOMATABLE-v1] |
| EC-047 | OOM / SIGKILL mid-run | Exit-signal classified infra; cleanup verified; no credential residue on host; artifacts discarded not partially stored | `scenarios/fleet_oom_midrun_test.go` | S2 | [AUTOMATABLE-v1] |
| EC-048 | Provider unavailable (simulated provider down) | Circuit breaker opens; work queues with backpressure; degradation visible in status; NO fabricated results or silent loss | `integration/fleet_provider_unavailable_test.go` | S2 | [AUTOMATABLE-v1] |
| EC-049 | Credential over-scope attempt: runner requests access beyond declared action/repo/env/TTL (invariant #4) | Issuance denies; runtime scope check enforces; attempt audited; blast radius of compromised runner = one job | `invariants/fleet_credential_scope_test.go` | S0 | [AUTOMATABLE-v1] |

## Multi-tenancy (EC-050 – EC-055)

| ID | Description | Expected correct behavior | Coverage suggestion | Sev | Flag |
|---|---|---|---|---|---|
| EC-050 | Cross-tenant reads by sequential-ID guessing | Opaque IDs + tenant-scoped queries + authz; response for other-tenant ID indistinguishable from nonexistent (uniform 404) | `integration/mt_idor_guessing_test.go` | S0 | [AUTOMATABLE-v1] |
| EC-051 | Per-tenant budget enforcement under contention | Hard isolation: tenant A saturation cannot consume tenant B reserved capacity; accounting exact | `scenarios/mt_budget_isolation_test.go` | S1 | [AUTOMATABLE-v1] |
| EC-052 | Noisy neighbor: one agent storms thousands of candidates | Admission throttling + per-agent concurrency caps; other tenants' latency SLOs hold throughout the storm | `scenarios/mt_noisy_neighbor_test.go` | S1 | [AUTOMATABLE-v1] |
| EC-053 | Tenant deleted while its candidates are running | Graceful teardown of leases/jobs; retention policy honored; no dangling leases or orphan compute billed | `integration/mt_tenant_deleted_midflight_test.go` | S1 | [AUTOMATABLE-v1] |
| EC-054 | Cross-tenant cache/artifact poisoning | Cache keys namespaced by tenant/trust-domain; cross-tenant fetch denied even for byte-identical content | `integration/mt_cache_isolation_test.go` | S0 | [AUTOMATABLE-v1] |
| EC-055 | Quota boundary race: last CPU-minute claimed concurrently | Atomic reservation; oversubscription impossible; losers queue cleanly | `invariants/mt_quota_boundary_race_test.go` | S2 | [AUTOMATABLE-v1] |

## Data / ledger (EC-056 – EC-064)

| ID | Description | Expected correct behavior | Coverage suggestion | Sev | Flag |
|---|---|---|---|---|---|
| EC-056 | Hash-chain break detection (tampered or missing event) | Chain verifier detects prev-hash/linkage break; projections halt fail-closed; page on-call; read-only mode available | `integration/data_chain_break_test.go` | S0 | [AUTOMATABLE-v1] |
| EC-057 | Projection rebuild correctness: rebuild diverges from live projection | Equivalence property: rebuild-from-genesis == live projection for ANY event history; mismatch alarms and BLOCKS decisions until resolved | `invariants/data_projection_equivalence_test.go` | S0 | [AUTOMATABLE-v1] |
| EC-058 | Clock skew between event sources reorders causal sequence | occurred_at vs received_at kept separate; ordering driven by causal references, never raw source clocks alone | `invariants/data_clock_skew_ordering_test.go` | S2 | [AUTOMATABLE-v1] |
| EC-059 | ID collision attempt (forged reuse of existing ULID) | Uniqueness constraints reject conflicting create; logged as potential tampering, not silent overwrite | `integration/data_id_collision_test.go` | S2 | [AUTOMATABLE-v1] |
| EC-060 | Migration rollback safety (rollback with live event history) | Expand-contract discipline enforced; rollback rehearsed against prod-like ledger snapshot in CI; zero event loss | `scenarios/data_migration_rollback_test.go` | S1 | [MANUAL/LATER] |
| EC-061 | Schema version drift: old producer events read by newer consumer | Versioned envelopes + upcasters tested per version pair; unknown major version fails safe (park), never misinterpreted | `integration/data_schema_drift_test.go` | S2 | [AUTOMATABLE-v1] |
| EC-062 | At-least-once processing double-applies a business effect | Every effect idempotent, keyed on event ID dedupe table; property: applying any multiset (with arbitrary duplicates/subsets) converges to identical state | `invariants/data_idempotent_apply_property_test.go` | S0 | [AUTOMATABLE-v1] |
| EC-063 | Cancellation processed after MergeAuthorized (post-terminal event) | Terminal-state machine logs-and-ignores late effects; nothing killed or invalidated after authorization | `invariants/data_post_terminal_events_test.go` | S0 | [AUTOMATABLE-v1] |
| EC-064 | Torn/partial event write (disk full, crash mid-insert) | Transactional appends only; readers never observe half-events; gap detection triggers rebuild verification | `scenarios/data_torn_write_test.go` | S1 | [AUTOMATABLE-v1] |

---

## Summary

64 rows · 60 [AUTOMATABLE-v1] · 4 [MANUAL/LATER] (EC-009, EC-017, EC-041, EC-060).
Severity mix: 24 × S0, 19 × S1, 15 × S2, 6 × S3. All five design-doc invariants map to dedicated invariant-level suites (EC-020, EC-033, EC-034, EC-035, EC-049).
