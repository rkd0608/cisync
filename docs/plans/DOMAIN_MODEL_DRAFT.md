# CISync — Domain Model & API Draft (v1)

Status: DRAFT · Owner: Domain Model & API Architect · Inputs: product spec, `ARCHITECTURE_DRAFT.md` (events/envelope/storage), `EDGE_CASES_DRAFT.md` (races/invariants). Freezes aggregates, relations, ladder, priority math, REST v1, event payloads, dossier/decision formats, policy model. Becomes `packages/contracts/openapi.yaml` (§5) + `events.schema.json` (§6) after synthesis.

---

## 0. Conventions & invariant register

- IDs: ULID text with prefixes `int_ cand_ clus_ val_ run_ ev_ fc_ lease_ repair_ pol_ dec_` (+ `evt_ org_ corr_`). Spec examples `ci_01J…`/`env_9f1…` are illustrative; canonical prefixes above win.
- Timestamps `timestamptz` UTC; ordering logic uses ledger `seq`, never wall clocks (EC-031/EC-058).
- Every mutation = ledger event (envelope per ARCHITECTURE_DRAFT §4.1); projections rebuildable. Mutating API calls require `Idempotency-Key`.
- Gate legend for state machines: **A** = automatic (system, no approval), **P** = policy-gated (allowed only if active Policy permits at current autonomy level), **H** = human-gated.

Invariant register (executable per TEST_STRATEGY_DRAFT; referenced below):

| ID | Invariant |
|---|---|
| I-01 | A skipped/quarantined test never counts as positive evidence |
| I-02 | Evidence reused only when full `inputs_hash` matches (base SHA, lockfiles, flags, toolchain) |
| I-03 | One accepted EvidenceRecord per job lease attempt (unique `(run_id, attempt)` on accept) |
| I-04 | Runner credentials scoped ≤ declared action/repo/env/TTL |
| I-05 | Repair patches confined to contract `allowed_paths`, enforced server-side |
| I-06 | Budget conservation: Σ reservations − Σ releases = current usage, always |
| I-07 | Ledger append-only + hash-chained; projections provably equivalent to replay |
| I-08 | Post-terminal events logged-and-ignored on terminal aggregates |
| I-09 | Every Decision carries the `policy_id`+`policy_version` that produced it; no resolvable active policy ⇒ fail closed, no decision rendered |

---

## 1. Aggregates

Types: `ulid` = prefixed text ID; `ts` = timestamptz; `glob[]` = array of glob patterns; enums as `a|b|c`.

### 1.1 Intent (`int_`)
Fields: `id`, `tenant_id:org_`, `repo:text`, `base_ref:text`, `base_snapshot:sha256`, `goal:text`, `constraints:string[]`, `acceptance_criteria:string[]`, `owned_surfaces:glob[]`, `risk_class:low|medium|high|critical`, `deadline:ts?`, `origin:agent_api|cli|github_webhook|synthetic`, `agent_lineage:string[]`, `compute_budget{cpu_minutes:int, environment_minutes:int, repair_attempts:int}`, `resolved_policy{policy_id,policy_version}`, `state`, `created_at`, `closed_at?`.

States (= Change Graph UI states): `exploring → validating → blocked ⇄ repairing → merge_ready → deploying → monitoring → completed | rejected`.

| From → To | Gate | Trigger |
|---|---|---|
| exploring → validating | A | first Candidate admitted + ValidationPlan built |
| exploring → rejected | P | admission denied (prohibited surfaces, unclassifiable risk) |
| validating → blocked | A | non-transient FailureCase; budget exhausted (reason=budget); merge-base stale |
| validating → repairing | P | repair authorized (autonomy ≥3, class repairable) |
| repairing → validating | A | repaired patch passes contract check + resubmitted |
| blocked → validating | A | base refreshed + invalidated evidence rerun; **H** variant: human unblock override |
| validating → merge_ready | P if autonomy ≥4 & low/medium risk, else H | plan satisfied + Decision=eligible rendered |
| merge_ready → deploying | P if autonomy 6, else H | deploy authorization per policy |
| deploying → monitoring | A | deploy executed, canary watch started |
| monitoring → completed | A | required post-merge evidence satisfied |
| any pre-terminal → rejected | P/H | dominated/superseded, deadline timeout (outcome reason=timeout, EC-016), security/policy violation, human reject |

Local invariants: exactly one active change-scope lease per active intent; `owned_surfaces ⊆ repo`; `compute_budget` immutable after grant (change ⇒ new lease version); `risk_class` immutable after first candidate (reclassify ⇒ new superseding intent).

### 1.2 Candidate (`cand_`)
Fields: `id`, `intent_id:int_`, `submitter:actor_ref`, `patch_ref:url_or_bundle_digest`, `head_sha`, `base_sha_at_submit`, `changed_paths:glob[]`, `changed_symbols:string[]?`, `est_cost_millicents:int`, `priority_score:numeric`, `priority_computed_at:ts`, `cluster_id:clus_?`, `relation_to_rep:duplicate_of|alternative_of|composable_with|conflicts_with|prerequisite_of|null`, `state`, `created_at`.

States: `submitted → planned → validating → eligible | rejected`; side-states `blocked_representative`, `repairing`; terminal: `eligible, rejected, superseded, cancelled`.

| From → To | Gate | Trigger |
|---|---|---|
| submitted → planned | A | ValidationPlan constructed |
| planned → validating | P | scheduler admits within WIP caps + budgets (I-06) |
| validating → repairing | P | repair authorized for attributable deterministic failure |
| repairing → validating | A | repair patch passes server-side contract check (I-05) |
| validating → eligible | P/H | Decision `eligible_for_merge_train` rendered |
| validating → rejected | P/H | terminal failure class, policy violation, human reject |
| planned/validating → blocked_representative | A | clustered as duplicate; representative elected |
| blocked_representative → superseded | A | representative eligible (intent solved); partial evidence kept as diagnostics only (EC-014) |
| any pre-terminal → cancelled | A | intent closed/expired, lease revoked/expired, deadline hit |

Local invariants: intent pre-terminal at submit (else 409 LateSubmission, EC-013); `head_sha ≠ base_sha_at_submit`; ≤1 live candidate per `(intent_id, head_sha)` (identical SHAs dedupe to shared lineage, EC-019); priority recomputed on base advance, relation change, or telemetry refresh tick.

### 1.3 Cluster (`clus_`)
Fields: `id`, `repo_id`, `strategy_version:text` (clustering algo version), `rep_candidate_id:cand_`, `member_count:int`, `state:forming|active|dissolved`, `created_at`.
Transitions: `forming → active` A (rep elected = argmax priority among members); `active → dissolved` A (≤1 member, or all terminal). Rep re-election after rep failure = RESERVED (`representative.promoted`); until then failed clusters dissolve and re-form.
Local invariants: candidate in ≤1 active cluster per repo; `rep_candidate_id` always a live member; membership frozen once all members terminal.

### 1.4 ValidationPlan (`val_`)
Fields: `id`, `candidate_id:cand_`, `policy{policy_id, policy_version}`, `tiers:[{tier:0-4, jobs:[job_spec], rationale:text, selection_confidence:numeric?}]`, `required_evidence_kinds:string[]`, `inputs_hash:sha256` (base SHA + lockfiles + flags + toolchain), `state:active|satisfied|invalidated|superseded`, `created_at`.
Transitions: `active → satisfied` P (evaluator confirms all required kinds accepted; I-01 enforced at accept-time); `active → invalidated` A (`merge_base.advanced` or inputs drift ⇒ `evidence.invalidated` cascade); `invalidated → active` A (replan = new plan version; old rows retained); `active → superseded` A (candidate superseded/cancelled).
Local invariants: cites its policy version (feeds I-09); `selection_confidence` present whenever a tier defers suites (drives fallback §3); immutable once superseded/invalidated.

### 1.5 ValidationRun (`run_`) — the job unit (refines arch-draft `validation_requests` + fleet `execution_jobs`)
Fields: `id`, `plan_id:val_`, `candidate_id:cand_`, `tier:0-4`, `job_spec:jsonb`, `attempt:int` (from 1), `pool:text`, `est_duration_ms:int`, `est_cost_millicents:int`, `cancellation_conditions:jsonb`, `fence_token:int`, `state:queued|dispatched|running|succeeded|failed|timed_out|cancelled`, `logs_digest?`, `artifact_digests:string[]?`, `started_at?`, `finished_at?`.
Transitions: `queued → dispatched` A (idempotent on `(id, fence_token)`, EC-028/29); `dispatched → running` A (worker claim); `running → succeeded|failed` A (results uploaded before ack); `running → timed_out` A (budget enforcer, EC-044); `failed → queued` P (bounded infra-transient retry: `attempt++`, **fresh** fence, EC-042); `queued|dispatched|running → cancelled` A (supersede/staleness propagation; late results = diagnostics only, EC-014/027).
Local invariants: accepts results only from current `fence_token` holder (EC-046); cancel-after-complete ignored (I-08); ≤1 accepted EvidenceRecord per `(id, attempt)` (I-03).

### 1.6 EvidenceRecord (`ev_`)
Fields: `id`, `run_id:run_`, `candidate_id:cand_`, `kind:text` (vocabulary shared with policy `required_evidence`: `hermetic_build, api_compat, payment_contract, idempotency_race, sast_diff, selected_unit, migration_compat, …`), `verdict:pass|fail`, `status:proposed|accepted|rejected|invalidated`, `digests:sha256[]`, `inputs_hash:sha256`, `confidence:numeric`, `cost_millicents:int`, `produced_by_lease:lease_`, `accepted_at?`, `invalidated_reason?`.
Transitions: `proposed → accepted` P (validator checks provenance binding lease↔SHA↔digests; I-01/I-02/I-03; EC-036–038); `proposed → rejected` A (any check fails; tamper ⇒ quarantine + security alert); `accepted → invalidated` A (TTL expiry or `inputs_hash` mismatch checked at decision time, EC-032/034).
Local invariants: I-01/I-02/I-03 structural; accepted records never deleted — invalidation is a state (audit trail preserved).

### 1.7 FailureCase (`fc_`)
Fields: `id`, `candidate_id:cand_`, `run_id:run_`, `signature_digest:sha256` (normalized log fingerprint), `classification:infra_transient|known_flake|probable_flake|compile_regression|test_expectation_drift|functional_regression|merge_conflict|security_policy_violation|timeout`, `classification_confidence:numeric`, `reproduction_command:text`, `causal_signals:string[]`, `suspected_paths:glob[]`, `routed_action:retry|quarantine_flake|repair|escalate_human|reject|none`, `state:open|classified|routed|closed`, `created_at`.
Transitions: `open → classified` A (taxonomy engine; flake classes require forensics corroboration, EC-039); `classified → routed` P (class × policy routing table; security/policy violations never auto-waived); `routed → closed` A (downstream terminal: retries exhausted / RepairTask closed / escalation resolved).
Local invariants: classification immutable once set (correction = new case citing old); `routed_action=repair` requires an existing RepairTask before close; timeout is distinct from product regression (EC-016/EC-044).

### 1.8 EnvironmentLease (`lease_`) — unified change/env lease (spec's `lease_id`; also preview-env leases)
Fields: `id`, `scope{kind:change_scope|environment, surfaces:glob[], env_template?:text}`, `holder:actor_ref`, `budget{cpu_cores, mem_gb, environment_minutes, preview_urls}`, `ttl_expires_at:ts`, `renewal_count:int`, `queue_position?:int`, `eta?:ts`, `state:requested|granted|released|expired|revoked`.
Transitions: `requested → granted` P (capacity + template `max_concurrent` + tenant budget, I-06); `granted → granted` A (heartbeat renewal ≤ cap); `granted → released` A (explicit completion; release idempotent, EC-030); `granted → expired` A (TTL sweeper); `granted → revoked` A (supersede / tenant teardown, EC-009/053).
Local invariants: budget reservation/release exactly-once (I-06); expired/revoked leases cannot renew (fresh grant required); change-scope lease auto-revoked when holder intent reaches terminal state.

### 1.9 RepairTask (`repair_`)
Fields: `id`, `failure_case_id:fc_`, `candidate_id:cand_`, `envelope{reproduction_command, failed_assertion?, suspected_diff_hunks:string[], allowed_paths:glob[], prohibited_paths:glob[], max_iterations:int, required_evidence_after_repair:string[]}` (= spec's repair envelope), `attempts_used:int`, `resulting_patch_refs:string[]`, `state:authorized|dispatched|iterating|applied|exhausted|aborted`.
Transitions: `authorized → dispatched` P (autonomy ≥3 and class repairable); `dispatched → iterating` A; `iterating → applied` P (patch passes server-side contract check I-05 AND resubmission accepted as new candidate revision); `iterating → exhausted` A (`attempts_used == max_iterations`, escalation set, EC-021); `authorized|dispatched|iterating → aborted` A (cancel/supersede mid-loop; budget reconciled exactly once, EC-015).
Local invariants: every attempt diff validated against `allowed_paths` before acceptance (I-05); `attempts_used` monotonic; `applied`/`exhausted`/`aborted` terminal — no double-close.

### 1.10 Policy (`pol_`) — body defined in §8
Aggregate fields: `id`, `version:int` (monotonic per policy family), `status:draft|active|retired`, `body:jsonb` (§8), `activated_by:actor_ref?`, `activated_at?`.
Transitions: `draft → active` H (policy owner approves; shadow-mode report attached); `active → retired` H (superseded by newer active version). Immutable once active — amendments create a new version.
Local invariants: ≤1 active version per family at any instant (deterministic resolution for I-09); retired versions readable forever (past-decision audit).

### 1.11 Decision (`dec_`)
Fields: `id`, `subject{type:intent|candidate|integration_set, id}`, `verb:eligible_for_merge_train|rejected|deferred|repair_required|combine|split|quarantine|deploy`, `confidence:numeric`, `policy{policy_id, policy_version}` (I-09), `explanation{summary:text, factors:[{name, value, source}], skipped_evidence:[{kind, reason}]}`, `evidence_refs:ev_[]`, `inputs_hash:sha256`, `causation_id:evt_`, `rendered_at:ts`.
States: `rendered` — immutable fact, no transitions. Human override = RESERVED (`policy.override_requested` creates a NEW Decision superseding the old; old never mutated).
Local invariants: I-09 fail-closed; `inputs_hash` freshness re-checked at render (EC-032); v1 verb subset = `eligible_for_merge_train | rejected | deferred` (rest Wave 3+, per ARCHITECTURE_DRAFT §10).

---

## 2. Relations

**Intent ↔ Candidates:** strictly 1:N. Intent closes when: one candidate eligible and the rest superseded/cancelled; or all candidates terminal; or deadline. Synthetic intents (PR without declared intent) follow the same model with `origin=github_webhook`.

Candidate ↔ Candidate relations (directed edge `relation_to_rep`, plus cluster membership):

| Relation | Semantics | Scheduling implication | Superseding implication |
|---|---|---|---|
| `duplicate` | same underlying fix (symbol overlap ≥ θ AND similarity ≥ τ_dup) | only representative gets >Tier-1 compute; others held `blocked_representative` | rep eligible ⇒ duplicates `superseded(by=rep, reason=dominated_duplicate)`; rep terminal failure ⇒ RESERVED re-election promotes best duplicate |
| `alternative` | competing solutions, mutually exclusive at integration | bounded tournament: ≤K concurrent validations (default K=3), ranked by priority | losers superseded when any alternative renders eligible; cap prevents PR proliferation |
| `composable` | independent, likely co-mergeable (disjoint surfaces, no dep-path intersection) | scheduled independently; flagged as future integration-set composition (W3) | neither supersedes other; composition still fully validated at Tier 4 — relation label never waives validation (EC-017) |
| `conflicting` | high predicted merge-conflict probability (same owned surface / overlapping hunks) | serialized: lower-priority candidate held blocked until higher resolves or releases surface; `recommendation=coordinate` shown to both agents | no auto-supersede; loser withdraws or rebases; edges re-evaluated on every base advance |
| `prerequisite` | A builds on B's merge (A's dep-graph edge exists only via B) | B gets urgency boost (×1.25 default); A's Tier 3/4 deferred until B eligible | B rejected ⇒ A auto-blocked (reason=prerequisite_failed), not superseded — may retarget |

Intent-level overlap surfaced as `conflicts[]` in `POST /v1/change-intents` is the same machinery from the intent side: `relation=overlapping` iff owned surfaces intersect; entry = `{intent_id, relation, owner, recommendation}` (spec shape).

Cluster membership rules: (1) assignment at candidate submission against active clusters of same repo — join iff path-overlap ≥ θ (default 0.6) AND similarity ≥ τ (trigram v0; embeddings later, pending sign-off per ARCHITECTURE_DRAFT §9), else new cluster; (2) relation-to-rep recomputed on rep change; (3) cross-cluster `conflicting` edges between reps consulted by scheduler serialization; (4) `strategy_version` stamped so historical clusters stay interpretable after algorithm changes.

---

## 3. Validation ladder mapping

| Tier | Name | Default jobs | Timeout | Cost est. | Promotion rule to next tier |
|---|---|---|---|---|---|
| 0 | Admission | secret scan, format/lint, typecheck-lite, diff sanity, policy admissibility | 60 s | ≤$0.05 | all pass → auto-promote; fail ⇒ reject (no retry) |
| 1 | Local impact | compile affected targets, selected unit tests (impact model), SAST diff scan | 15 min | ~$0.20 | pass AND `selection_confidence ≥ 0.98` → T2; deterministic fail ⇒ FailureCase routing |
| 2 | Contract | impacted integration tests, API-compat, schema/migration compat, dependency/license check | 30 min | ~$1.50 | pass AND all policy-required contract evidence accepted → T3 if risk demands it |
| 3 | System | E2E/browser/mobile, load, fuzz on affected surfaces, preview env via EnvironmentLease(kind=environment) | 60 min | ~$12 | pass → T4 when candidate is merge-track (integration set / merge train); else stop at "eligible" per policy |
| 4 | Integration | rebase/merge-train simulation against projected post-merge state, integrated build+sign, canary gate (W3) | 90 min | ~$40 | produces Decision `eligible_for_merge_train` / deploy authorization |

Gate notes: Tier 0→1→2 promotions are A (automatic) once jobs green. Entry to Tier 3 is P (policy: only `risk_class ∈ policy.tier3_risk_classes` or explicit uncertainty fallback). Tier 4 entry is P and additionally H when autonomy <4 or risk ≥ high.

**Fallback-to-full-suite triggers** (any one widens selection to full suite for that tier):
1. `selection_confidence < policy.min_selection_confidence` (spec default: prediction uncertainty > 0.02);
2. sparse history: `< min_samples_for_selection` (default 20) observations for the touched surface class in the learned-stats table;
3. protected-path touch: auth/**, payments/**, migrations/**, infra/** (static config list);
4. any member of selected set has active flake signal or was quarantined in last 14 days;
5. relation=`conflicting` composition or integration-set assembly (composition validation, EC-017);
6. ambiguous failure after bounded retry (failure class confidence < 0.8);
7. explicit policy override / human request.
Fallback runs are recorded with `rationale=fallback:<trigger>` in the ValidationPlan so decisions remain explainable (I-09).

---

## 4. Priority formula operationalization

Frozen form (scheduler ranks ValidationRuns; candidate priority = max over its queued runs):

```
priority(run) = P(decision_changes | run)
              * risk_reduction(cand)
              * urgency(cand)
              * business_value(repo, intent)
              / ( cost_est(run) * contention_penalty(pool) )
aging_floor:   effective_priority = max(priority, floor(age)) ; floor(age) = age_minutes * aging_slope (default 0.01/h, cap 0.5)
```

| Factor | Computation | Source | Sparse-data default |
|---|---|---|---|
| `P(decision_changes)` | historical rate that this job kind flipped an outcome for similar changes: key `(job_kind, surface_class, risk_class)` | learned stats table (`stats.test_outcomes`, updated from ledger nightly) | 0.5 (max-uncertainty ⇒ run it) |
| `risk_reduction` | static map by risk_class × blast_radius_factor = min(1, downstream_dependents/20); blast radius from dep graph | dep-graph projection + static config | high=1.0, medium=0.7, low=0.4 |
| `urgency` | `0.5 + 0.5*deadline_proximity` where proximity = clamp(1 − hours_to_deadline/72, 0, 1); ×1.25 if prerequisite-of others; + staleness term: base_age_hours × 0.005 (stale work rises) | Intent.deadline, cluster edges, base_sha age vs repo HEAD | 0.5 when no deadline |
| `business_value` | static tiering per repo/team/domain from policy scope selectors (`value_tier: 0.2–2.0`) | Policy.body.budgets.value_tiers + repo adapter YAML | 1.0 |
| `cost_est` | p50 historical duration × pool unit rate; falls back to adapter-declared estimate, then tier default (§3 table) | learned stats → manifest → §3 constants | §3 defaults |
| `contention_penalty` | live telemetry: `(queue_depth{pool,tier}+1)/(capacity{pool}+1)` recomputed each scheduling tick | fleet gauges (`fleet_queue_depth`) | 1.0 |

Deterministic tie-breaking (EC-024): equal effective_priority ⇒ older `created_at` first ⇒ lexicographically smaller ULID. Ordering derived from ledger logical time, wall clocks advisory (EC-031). Aging floor guarantees dispatch bound (EC-025); slope/cap are policy-tunable.

Recompute points: candidate submission, any evidence accept/invalidate, cluster relation change, `merge_base.advanced`, every scheduler tick for contention term only (others cached until invalidation).

---

## 5. REST API v1 surface

Base: `https://api.sauron…/v1`. All requests carry tenant auth header; mutating ones require `Idempotency-Key`. Canonical paths follow the spec's Agent Integration API; ARCHITECTURE_DRAFT §2.1's shorter `/v1/intents` forms ship as aliases (same handlers), to be reconciled at synthesis.

| # | Method & path | Request → Response (sketch) | Errors | Scope |
|---|---|---|---|---|
| 1 | POST `/change-intents` (alias POST `/intents`) | `{goal, repository, base, expected_surfaces[], acceptance_criteria[], constraints?, risk, deadline?}` → `200 IntentGrant` (below) | 400 validation_failed · 409 surfaces_prohibited · 429 rate_limited/budget_exceeded | CORE-v1 |
| 2 | GET `/change-intents/{id}` | → `Intent` + state + evidence_completeness_pct | 404 (uniform, EC-050) | CORE-v1 |
| 3 | GET `/change-intents/{id}/candidates` | → `CandidateSummary[]` incl. cluster/relation | 404 | CORE-v1 |
| 4 | POST `/change-intents/{id}/candidates` | `{patch_ref|bundle_digest, head_sha, base_sha}` → `201 {candidate_id, plan_summary{tiers, deferred[]}, lease_id}` | 400 · 409 intent_closed/LateSubmission (EC-013) · 409 duplicate_head_sha (EC-019) | CORE-v1 |
| 5 | GET `/candidates/{id}` | → `Candidate` + priority + queue_position | 404 | CORE-v1 |
| 6 | GET `/candidates/{id}/dossier` | → EvidenceDossier JSON (§7) | 404 | CORE-v1 |
| 7 | GET `/clusters/{id}` | → `Cluster` + members + relations + rep | 404 | CORE-v1 |
| 8 | POST `/leases/{id}/renew` | `{ttl_seconds?}` → `{lease_id, ttl_expires_at, renewal_count}` | 409 expired/revoked (fresh grant required) | CORE-v1 |
| 9 | DELETE `/leases/{id}` | → `204` (release, idempotent) | — | CORE-v1 |
| 10 | GET `/events?after_seq={seq}&types=a,b&aggregate={type}:{id}` | → `{events: Envelope[], next_seq}` ledger tail for agents/web sync | 416 bad after_seq | CORE-v1 |
| 11 | POST `/hooks/github` | GitHub webhook (HMAC) → `202` always (async processing) | 401 bad_signature (EC-003) · 413 too_large | CORE-v1 |
| 12 | GET `/healthz`, GET `/metrics` | liveness / Prometheus | — | CORE-v1 |
| 13 | POST `/failure-cases/{id}/escalate` | `{note}` → `202` sets routed_action=escalate_human | 409 not_classified | LATER (W2) |
| 14 | POST `/policies` / POST `/policies/{id}:activate` | Policy body (§8) → `{policy_id, version}` | 409 version_conflict | LATER (v1 ships built-in default policy only) |
| 15 | POST `/decisions/{id}:override` | `{reason}` → new superseding Decision | 403 beyond_autonomy | LATER (RESERVED event `policy.override_requested`) |
| 16 | POST `/integration-sets` (merge-train compose) | `{candidate_ids[]}` → IntegrationSet | 409 conflicting_relation | LATER (W3) |

**`IntentGrant` response** (matches spec example exactly):

```json
{
  "intent_id": "int_94f8",
  "lease_id": "lease_3ab1",
  "base_snapshot": "main@b734e",
  "worktree_or_branch": "agent/int_94f8/candidate_01",
  "allowed_paths": ["services/checkout/**", "libs/idempotency/**"],
  "prohibited_paths": ["infrastructure/prod/**"],
  "conflicts": [
    {"intent_id": "int_91c2", "relation": "overlapping", "owner": "payments-platform", "recommendation": "coordinate"}
  ],
  "required_evidence": ["payment-contract", "idempotency-race"],
  "compute_budget": {"cpu_minutes": 120, "environment_minutes": 30, "repair_attempts": 2},
  "queue_position": 3,
  "eta_seconds": 90
}
```

**Machine-readable error envelope** (all non-2xx):

```json
{"error": {"code": "budget_exceeded", "message": "tenant cpu-minute budget exhausted", "details": {
  "scope": "tenant:org_01J", "kind": "cpu_minutes", "limit": 5000, "consumed": 5000, "resets_at": "2026-08-23T04:00:00Z"
}, "retry_after_s": 600, "suggestions": ["reduce expected_surfaces", "defer non-urgent intents", "request quota increase"]}}
```

Error codes: `validation_failed(400) unauthorized(401) forbidden(403) not_found(404, uniform for cross-tenant) conflict_state(409, details.reason∈late_submission|intent_closed|expired_lease|duplicate_sha|version_conflict) idempotent_replay(200, original response) budget_exceeded(429, shape above) rate_limited(429, plain Retry-After) unavailable(503, Retry-After)`.

---

## 6. Event payloads (CORE events; envelope per ARCHITECTURE_DRAFT §4.1 — only `payload` bodies here)

| Event | Payload fields |
|---|---|
| `delivery.accepted` | `source:github|agent_api`, `ext_delivery_id`, `normalized_kind`, `repo`, `actor` |
| `intent.declared` | `goal`, `constraints[]`, `acceptance_criteria[]`, `owned_surfaces[]`, `risk_class`, `deadline?`, `origin`, `agent_lineage[]`, `resolved_policy{policy_id,policy_version}`, `compute_budget` |
| `lease.granted` | `lease_id`, `scope{kind,surfaces[],env_template?}`, `holder`, `budget{...}`, `ttl_expires_at`, `conflicts[]`, `required_evidence[]`, `queue_position?` |
| `lease.renewed`(=grant self-loop) | `lease_id`, `ttl_expires_at`, `renewal_count` |
| `lease.revoked` / `lease.expired` | `lease_id`, `reason:superseded|tenant_teardown|repo_deleted|ttl`, `released_budget{...}` |
| `candidate.submitted` | `candidate_id`, `intent_id`, `submitter`, `patch_ref`, `head_sha`, `base_sha`, `changed_paths[]`, `est_cost_millicents` |
| `cluster.assigned` | `cluster_id`, `candidate_id`, `rep_candidate_id`, `relation`, `similarity_score`, `strategy_version` |
| `validation.planned` | `plan_id`, `candidate_id`, `tiers[{tier,jobs[],rationale,selection_confidence?}]`, `required_evidence_kinds[]`, `inputs_hash`, `policy_version` |
| `validation.requested` | `run_id`, `plan_id`, `tier`, `est_duration_ms`, `est_cost_millicents`, `priority`, `cancellation_conditions`, `pool` |
| `validation.cancelled` | `run_ids[]`, `reason:superseded|stale_base|budget|intent_closed`, `causation_id` |
| `validation.started` | `run_id`, `attempt`, `fence_token`, `worker_id`, `provider` |
| `validation.completed` | `run_id`, `attempt`, `status:succeeded|failed|timed_out`, `logs_digest`, `artifact_digests[]`, `duration_ms`, `actual_cost_millicents` |
| `evidence.recorded` | `ev_id`, `run_id`, `candidate_id`, `kind`, `verdict`, `digests[]`, `inputs_hash`, `confidence`, `cost_millicents` |
| `evidence.invalidated` | `ev_ids[]`, `reason:base_advanced|toolchain_changed|ttl_expired`, `replan_plan_id?` |
| `merge_base.advanced` | `repo`, `old_sha`, `new_sha`, `affected_candidate_ids[]` |
| `failure.classified` | `fc_id`, `run_id`, `classification`, `confidence`, `signature_digest`, `suspected_paths[]`, `reproduction_command` |
| `repair.authorized` | `repair_id`, `fc_id`, `envelope{reproduction_command, failed_assertion?, suspected_diff_hunks[], allowed_paths[], prohibited_paths[], max_iterations, required_evidence_after_repair[]}` (= spec repair envelope) |
| `repair.completed` | `repair_id`, `outcome:applied|exhausted|aborted`, `attempts_used`, `new_patch_refs[]`, `required_evidence_status{kind:accepted|pending}[]` |
| `candidate.superseded` | `candidate_id`, `by_candidate_id`, `relation`, `reason:dominated_duplicate|tournament_loser|intent_solved` |
| `candidate.cancelled` | `candidate_id`, `reason:intent_closed|deadline|lease_lost|prerequisite_failed` |
| `decision.rendered` | `decision_id`, `subject{type,id}`, `verb`, `confidence`, `policy{policy_id,policy_version}`, `explanation{summary,factors[],skipped_evidence[]}`, `evidence_refs[]`, `inputs_hash` |

RESERVED events (W2/W3: `github.check_published`, `artifact.reused`, flake quarantine, `representative.promoted`, `integration_set.*`, deploy loop, `policy.override_requested`) get payload definitions at their wave cut-in; envelope stays stable.

---

## 7. Evidence dossier & decision record formats

`GET /v1/candidates/{id}/dossier` — exact shape (mirrors the spec's dossier example; `evidence_deferred[].reason` and `required_post_merge[]` are mandatory sections, possibly empty):

```json
{
  "candidate_id": "cand_01J…",
  "intent_id": "int_94f8",
  "generated_at": "2026-08-23T03:41:00Z",
  "inputs_hash": "sha256:9f1c…",
  "decision": {
    "decision_id": "dec_01J…",
    "verb": "eligible_for_merge_train",
    "confidence": 0.94,
    "policy": {"policy_id": "pol_payments_high_risk", "version": 4},
    "summary": "Eligible for merge train; full browser E2E deferred to canary stage"
  },
  "evidence_accepted": [
    {"ev_id": "ev_4482", "kind": "hermetic_build", "verdict": "pass", "digests": ["sha256:…"]},
    {"ev_id": "ev_4485", "kind": "api_compat", "verdict": "pass", "digests": []},
    {"ev_id": "ev_4490", "kind": "selected_unit", "verdict": "pass", "meta": {"selected": 44, "skipped_as_non_evidence": 1842}},
    {"ev_id": "ev_4493", "kind": "payment_contract", "verdict": "pass", "digests": ["sha256:…"]},
    {"ev_id": "ev_4501", "kind": "idempotency_race", "verdict": "pass", "meta": {"schedules": 10000}},
    {"ev_id": "ev_4506", "kind": "sast_diff", "verdict": "pass", "digests": []}
  ],
  "evidence_deferred": [
    {"kind": "browser_e2e", "reason": "no reachable UI/API dependency path from changed symbols", "stage_required": "canary"}
  ],
  "known_uncertainty": [
    {"description": "gateway-provider sandbox does not reproduce one production timeout mode",
     "mitigation": "2% canary plus duplicate-charge invariant monitor"}
  ],
  "required_post_merge": [
    {"kind": "canary", "params": {"traffic_pct": 2, "duration_minutes": 30}},
    {"kind": "invariant_monitor", "params": {"name": "duplicate_charge", "tolerance": 0}},
    {"kind": "error_rate_delta", "params": {"max_pp_vs_baseline": 0.2}}
  ]
}
```

Decision record (ledger payload of `decision.rendered`, and `GET /v1/decisions/{id}` when that endpoint lands in W2):

```json
{
  "decision_id": "dec_01J…",
  "subject": {"type": "candidate", "id": "cand_01J…"},
  "verb": "eligible_for_merge_train",
  "confidence": 0.94,
  "policy": {"policy_id": "pol_payments_high_risk", "version": 4},
  "explanation": {
    "summary": "All required high-risk evidence accepted; 2 suites deferred with reasons",
    "factors": [
      {"name": "selection_confidence", "value": 0.987, "source": "learned_stats:v3"},
      {"name": "risk_class", "value": "high", "source": "intent"},
      {"name": "inputs_fresh_at_render", "value": true, "source": "merge_base.advanced@seq10482"}
    ],
    "skipped_evidence": [{"kind": "browser_e2e", "reason": "non-material per policy path analysis"}]
  },
  "evidence_refs": ["ev_4482", "ev_4485", "ev_4490", "ev_4493", "ev_4501", "ev_4506"],
  "inputs_hash": "sha256:9f1c…",
  "causation_id": "evt_01J…",
  "rendered_at": "2026-08-23T03:41:00Z"
}
```

---

## 8. Policy model

Versioned documents (`pol_` aggregate §1.10). Resolution at every gate: most-specific active version wins by scope specificity (`paths` > `repos` > wildcard), ties → highest version. Every ValidationPlan, lease grant, repair authorization, and Decision stamps the resolved `{policy_id, policy_version}` ⇒ **I-09**: a decision without a resolvable active policy is never rendered (fail closed).

```json
{
  "policy_id": "pol_payments_high_risk",
  "version": 4,
  "scope_selectors": {
    "repos": ["acme/payments"],
    "paths": ["services/checkout/**", "libs/idempotency/**"],
    "risk_classes": ["high", "critical"],
    "actors": {"agents": ["agent:*"], "exclude": ["agent:docs-writer"]}
  },
  "required_evidence_by_risk": {
    "low":    ["hermetic_build", "selected_unit"],
    "medium": ["hermetic_build", "selected_unit", "api_compat"],
    "high":   ["hermetic_build", "api_compat", "payment_contract", "idempotency_race", "sast_diff"],
    "critical": ["hermetic_build", "api_compat", "full_integration", "human_approval"]
  },
  "ladder_overrides": {
    "tier3_risk_classes": ["high", "critical"],
    "min_selection_confidence": 0.98,
    "fallback_triggers": ["uncertainty_gt_0.02", "sparse_history_lt_20", "protected_paths"],
    "protected_paths": ["auth/**", "migrations/**", "infrastructure/prod/**"]
  },
  "budgets": {
    "per_candidate": {"cpu_minutes": 120, "environment_minutes": 30, "repair_attempts": 2},
    "per_tenant_hour": {"cpu_minutes": 5000, "concurrent_candidates": 40},
    "wip_by_tier": {"0": 200, "1": 60, "2": 20, "3": 6, "4": 2},
    "env_templates": {"payments-preview": {"max_concurrent": 4}},
    "value_tiers": {"acme/payments": 1.5, "acme/docs": 0.3}
  },
  "autonomy": {
    "level": 3,
    "levels_semantics": {
      "0": "observe and explain only",
      "1": "recommend tests/prioritization/cancellations; human acts",
      "2": "trigger pre-approved validation automatically",
      "3": "bounded repair attempts on isolated branches",
      "4": "mark low-risk candidates merge-eligible",
      "5": "auto-merge protected low-risk changes",
      "6": "progressive deploy under strict runtime invariants"
    },
    "auto_merge_risk_classes": [],
    "auto_repair_classes": ["compile_regression", "merge_conflict", "functional_regression"],
    "escalate_on": ["security_policy_violation", "test_expectation_drift", "classification_confidence_lt_0.8"]
  }
}
```

Autonomy ↔ state-machine gates: level gates which P transitions fire automatically vs escalate to H — merge_ready (needs ≥4), deploying (needs 6), repairing (needs ≥3 + class in `auto_repair_classes`). Levels are set per policy scope; kill switch = activate version with lower level (H-gated).

---

## 9. Open questions for other planners

1. **Evidence sufficiency %** (dossier header): exact formula over required-vs-accepted kinds + confidences — needs UI/test planner sign-off (also open in ARCHITECTURE_DRAFT §9).
2. **Policy source of truth for v1**: built-in default policy only (my assumption in §5 #14 LATER) vs repo adapter YAML compiled to a policy version — affects whether tenants get I-09-stamped custom policies on day 1.
3. **IntegrationSet as full aggregate?** Listed in Decision.subject but not defined here (W3). Needs its own state machine (composing→validating→merged→deploying) before merge-train work starts.
4. **Candidate revision identity**: repair resubmissions modeled as same candidate re-entering validating (current choice) vs explicit revision chain (`cand_x#2`) — affects evidence reuse keys and blame attribution.
5. **Lease unification risk**: merging change-scope and environment leases into one aggregate simplifies budget math but couples TTL semantics; fleet owner should confirm preview-env leases fit.
6. **Priority factor weights**: defaults above are static multipliers; who owns calibration against shadow-mode counterfactuals, and how often do learned-stats tables refresh?
7. **Cross-intent prerequisite detection**: dep-graph inference quality threshold before we trust auto-detected prerequisites for scheduling deferral (false positive = needless blocking).
8. **Decision confidence semantics**: raw probability vs calibrated score; needs agreement with shadow-mode evaluation metrics (TEST_STRATEGY_DRAFT L2).
