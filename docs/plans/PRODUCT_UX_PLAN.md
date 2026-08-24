# Product & UX Plan v0.2

Status: DRAFT · Owner: Product & UX Planner · Inputs: product spec ("The product in one view", onboarding journey Connect→Observe→Simulate→Optimize→Govern→Integrate), `ARCHITECTURE.md` FROZEN v1, `DOMAIN_MODEL_DRAFT.md` (§5 API, §7 dossier, §8 policy/autonomy). Planning only; ARCHITECTURE.md wins on conflict.
Alignment note: `GITHUB_APP_PLAN.md` not yet present at writing time; §3 below defines the check/comment surface that plan must implement, and §6 marks the endpoints it needs (`installations/status`, check deep-link fields).

---

## 1. Personas & journeys

### P1 — Platform admin (connects GitHub, watches shadow-mode trust)
Zero → value in one session:
1. Lands on `/` with no tenant → routed to **Connect wizard** (`/onboarding`). Sees the "what is/isn't copied" disclosure (source code: read-only metadata+diffs; secrets: never).
2. Step "Install GitHub App" → outbound link to GitHub App install page (repo-scoped, pilot repo recommended). Returns via `installation_callback` → **Installation status** screen shows webhook handshake state per repo (`pending → receiving_events`), last delivery seq.
3. Step "Choose posture" → readonly display of compiled-in default policy pack (v0.2 has no policy editing): autonomy level 0 (observe/explain), budgets, required-evidence-by-risk table. Checkbox "I understand nothing is enforced yet."
4. Finish → Dashboard. Board is empty; empty state teaches: "No intents yet. Open a PR or POST /v1/change-intents." Admin seeds traffic by opening a test PR.
5. Shadow mode (autonomy 0): every candidate dossier renders a **shadow banner**: "This decision was rendered but NOT published to GitHub. Autonomy level 0." Admin's daily loop = Dashboard risk groups + spot-opening dossiers + watching trust metrics strip (would-have-blocked count, would-have-missed count, decision latency).
6. Value moment: admin sees `Shadow verdicts: 41 eligible / 2 rejected / 0 conflicts with human CI outcomes over 7 days` and can justify raising autonomy to 1–2 later (policy activation is H-gated, out of UI scope v0.2 — surfaced as "contact operator / CLI").

Artifacts touched: Connect wizard, Installation status, Dashboard, Candidate dossier, shadow metrics strip, ledger event timeline (drill-down from any card).

### P2 — Service developer (normal PR)
1. Pushes branch, opens PR as always. No new tooling required.
2. GitHub shows one new check: **Agent Verification Gate** (§3). While validating: `in_progress — evidence 3/6 kinds accepted`.
3. Clicks "Details" → lands on `/candidates/[id]` permalink (deep-link convention §3.2) — the dossier they already have: decision banner, evidence_accepted list with digests, evidence_deferred WITH REASONS ("browser_e2e deferred: no reachable dependency path"), known_uncertainty, required_post_merge.
4. If synthetic intent (PR without declared intent): dossier header shows amber flag `origin=github_webhook (synthetic)` and lets them adopt it via CLI (`POST /change-intents` then link) — no blocking.
5. On failure: check goes `failed` with annotation pointing at the FailureCase classification ("deterministic_regression, confidence 0.91, repro command included") instead of log spelunking. If repair authorized: PR check returns to `in_progress — repair attempt 1/2 (scoped to services/checkout/**)`.
6. Value moment: green check whose summary line explains what ran, what was skipped and why — calibrated trust, not "✓ CI passed".

Artifacts touched: GitHub check row, candidate dossier permalink, (occasionally) intent detail, cluster view when their PR is flagged duplicate/conflicting of another agent's work.

### P3 — Coding-agent operator (declares intents, reads dossiers)
1. Works through Agent Integration API / future CLI, but monitors in web console.
2. Declares intent → opens `/intents/[id]`: goal, constraints, owned_surfaces, risk pill, state ladder (exploring→validating→…), conflicts panel listing overlapping intents with `recommendation=coordinate`, lease/budget meters (cpu_minutes used/120, repair_attempts 0/2).
3. Submits candidates → watches ladder advance; each candidate links to its dossier.
4. Reads dossiers like flight instruments: decision verb + confidence + factor list; skipped_evidence section tells the agent what it does NOT need to fix; known_uncertainty tells it what a reviewer will ask about.
5. On escalation (failure routing → escalate_human): intent enters `blocked`; operator sees the same pending item appear in the **Human decisions queue** and can add context via the escalation thread before a human rules.
6. Value moment: `explain` parity — everything the API returns is visible in UI with provenance (ledger tail link), so agent behavior and human oversight share one source of truth.

### P4 — Engineering manager (autonomy/budget dashboards)
1. Opens Dashboard, switches grouping to **risk** (default: state columns). Sees budget meters row: tenant cpu-minutes consumed vs limit, concurrent candidates vs WIP cap, environment leases active.
2. Autonomy panel (readonly v0.2): current level per policy family, what each level means (§8 semantics verbatim), counts of decisions auto-made vs escalated-to-human in trailing window.
3. Reviews **Human decisions queue** throughput: median time-to-human-decision, oldest pending item — this is where agents bottleneck, so it's an ops SLA surface.
4. Drills into any decision → dossier → ledger events (hash-chained audit trail; chain-verified badge from checkpoint verifier).
5. Value moment: answers "can we raise autonomy?" with shadow-mode evidence instead of vibes; answers "where is compute going?" without querying Postgres.

---

## 2. Screen inventory v0.2

Conventions: all data zod fail-closed (existing pattern); live updates via existing `/v1/events` tail (SSE/poll ≤1s); dense ops aesthetic (§7). "NEW" = route doesn't exist; "EVOLVE" = extends existing page.

### 2.1 Connect-GitHub onboarding wizard — NEW `/onboarding`
Purpose: zero-to-first-event in <10 min; set expectations honestly.
Data source: NEW `GET /v1/installations/status` (+ `POST /v1/installations/callback` handled by connector); default-policy render from NEW `GET /v1/policies/active` (readonly).
States: loading (steps skeleton) · error (GitHub OAuth denied / webhook timeout with retry affordance) · success (redirect dashboard).
```
┌ Connect Sauron ──────────────────────────────── ①②③ ─┐
│ ① Install App   ② Verify events   ③ Review posture   │
│ ──────────────────────────────────────────────────── │
│ WHAT WE RECEIVE            WHAT WE NEVER TOUCH       │
│ ✓ repo metadata + diffs    ✗ source storage          │
│ ✓ CI results & timings     ✗ secrets                 │
│ ✓ PR/check webhooks        ✗ merges, YAML, code      │
│ [ Install GitHub App → ]  (repo-select on GitHub)    │
│ Waiting: webhook handshake… ● acme/payments linked   │
└──────────────────────────────────────────────────────┘
```

### 2.2 Installation/repo status — NEW `/installations`
Purpose: prove the pipe is alive; first debugging stop when checks don't appear.
Data source: NEW `GET /v1/installations/status` → `{installation_id, account, repos:[{name, webhook_state: pending|receiving|stalled, last_delivery_seq, last_event_at}], app_permissions}`.
States: empty ("no installations — start at /onboarding") · stalled-repo warning row (red dot, "last delivery 34m ago") · dense table.
```
┌ Installations ────────────────── [resync] ───────────┐
│ acme (app installed ✓ checks:w contents:r prs:r …)   │
│ ├ payments      ● receiving   seq 48,112   12s ago   │
│ ├ docs-site     ● receiving   seq 9,204    3m ago    │
│ └ legacy-api    ○ STALLED    seq 40,981   34m ago ⚠   │
└──────────────────────────────────────────────────────┘
```

### 2.3 Dashboard — EVOLVE `/`
Purpose: air-traffic-control board (spec "The main screen"). Evolve current ledger-derived board.
Data source: existing `GET /v1/events` tail + existing projections via board lib; adds NEW `GET /v1/metrics/shadow` and budget rollup (NEW `GET /v1/budgets`) when available (graceful absence → hide strip, never block board).
Additions: filter bar (repo, risk_class, origin agent/human/synthetic); group-by toggle state-columns | risk-groups; budget meter strip; shadow-mode banner while autonomy=0; each card keeps evidence-completeness % (D8 formula) not pass/fail badge.
States: loading (existing) · teaching empty-state (§2.10) · dense (virtualized rows >200 cards) · stale-feed indicator if tail seq stops advancing >60s.
```
┌ Filters: [repo▾][risk▾][origin▾]  Group: state|risk ─┐
│ Budget ▓▓▓░ cpu 3100/5000 · cand 17/40 · env 2/4     │
│ SHADOW MODE — decisions rendered, nothing enforced   │
│ ─exploring─ ─validating─ ─blocked─ ─merge_ready─     │
│ ▣ int_94f8  ▣ int_94f8/A  ▣ int_77c1   ▣ int_51a0    │
│  HIGH 82%    HIGH 82%     MED  conflict  LOW 100%    │
│  2 cands     run T2 ⟳     w/ int_91c2  elig 0.94     │
└──────────────────────────────────────────────────────┘
```

### 2.4 Intent detail — EVOLVE `/intents/[id]`
Purpose: single intent cockpit. Existing: state ladder + conflicts panel.
Additions: **human action bar** (approve/unblock/reject) rendered ONLY when aggregate sits at an H-gated transition (per §5 map) and caller holds admin scope; lease/budget meters from IntentGrant-shaped projection; synthetic-origin flag; escalation thread when blocked-by-escalation.
Data source: existing `GET /v1/change-intents/{id}`, `GET /v1/change-intents/{id}/candidates`, `GET /v1/events?aggregate=intent:{id}`; actions → NEW `POST /v1/human-decisions/{id}:approve|reject|unblock`.
States: existing loading/error/not-found + action-pending (optimistic lock, 409 surfaces inline) · no-actions state hides bar entirely (never dead buttons).
```
┌ int_94f8  Fix duplicate payment retry   HIGH ▲      │
│ exploring─✓─validating─●─blocked─○─repairing─○─...  │
│ BLOCKED: awaiting human decision hd_7f2             │
│  reason: classification_confidence 0.74 < 0.80      │
│  [ Approve proceed ] [ Reject intent ] [ Unblock ↻ ]│
│ Conflicts: int_91c2 overlapping (payments-platform) │
│  → recommendation: coordinate                       │
│ Lease lease_3ab1 ▓▓▓▓▓▓▓░░░ 71/120 cpu-min          │
│ Candidates: A (rep, validating) B (alternative) C…  │
└──────────────────────────────────────────────────────┘
```

### 2.5 Candidate dossier — EVOLVE `/candidates/[id]`
Purpose: THE trust artifact. Existing sections stay (decision banner, evidence_accepted, evidence_deferred, known_uncertainty, required_post_merge).
Additions:
- **Shareable permalink**: canonical URL `/candidates/[id]` IS the permalink (immutable ULID; content regenerated but decision history append-only). Add "Copy evidence link" button producing `?at=dec_{decision_id}` variant that pins the rendered decision in the banner even after re-renders — safe to paste in PRs/slack. GitHub details_url uses this form (§3.2).
- Factor list expander on decision banner (from Decision.explanation.factors) — summary first, factors on expand.
- Ledger provenance footer: causation evt id + chain-verified badge.
Data source: existing `GET /v1/candidates/{id}/dossier` (§7 shape covers everything above except pinned-decision lookup — see gap G4).
States: loading · not-found · pre-plan (submitted, no val_ yet: show "validation plan forming") · superseded banner (link to rep candidate) · invalidated-evidence notice (amber, lists ev_ids + reason).

### 2.6 Human decisions queue — NEW `/decisions`
Purpose: one place for every H-gated escalation (failure routing, tier-4 entry at low autonomy, expectation drift, security violations). The EM/ops SLA surface.
Data source: NEW `GET /v1/human-decisions/pending` (+ `?scope=`); act via same approve/reject endpoint as 2.4.
States: empty ("Nothing waiting on humans. Agents are within policy.") — this empty state is a feature, style it as such · dense list sorted by age, aging rows shift toward red.
```
┌ Human decisions (3 pending) ── median resolve 4h12m ─┐
│ hd_7f2 ⚠ 6h old  int_94f8/cand_A                     │
│   ambiguous failure conf 0.74 · FC-2398              │
│   [open dossier] [approve] [reject]                  │
│ hd_7e1 2h old  int_88b0  test_expectation_drift      │
│   "behavior or test wrong?" · diff preview           │
│ hd_7d0 18m old  int_77c1  tier4 entry (low autonomy) │
└──────────────────────────────────────────────────────┘
```
Each action requires a reason string (goes to ledger as compensating event; audit trail visible inline under the resolved item).

### 2.7 Settings (policy, readonly) — NEW `/settings`
Purpose: make I-09 legible — users must SEE which policy produced which gate outcome.
Data source: NEW `GET /v1/policies/active` → active versions + full body (§8 JSON pretty-rendered: required_evidence_by_risk table, autonomy levels with semantics, budgets, protected paths, escalate_on list).
States: loading · single-default-policy (expected v0.2: banner "compiled-in default pack — editing ships with policy API W2+") · retired-version history accordion (read-only, supports past-decision audits).
No mutation controls in v0.2. Every policy field links a tooltip to the screens it affects ("this is why your Tier-3 entry needs a human").

### 2.8 First-run empty states that teach — cross-cutting
Every empty state answers three lines: **what this shows / why it's empty / one action to change that.**
- Dashboard empty → "No change activity yet. Open a PR on a connected repo, or POST /v1/change-intents (curl shown)." + link /onboarding if no installation.
- Queue empty → celebratory (see 2.6).
- Clusters empty → "Clustering forms when ≥2 candidates overlap ≥0.6 path-similarity."
- Dossier deferred-section empty → render literal "Nothing deferred — plan ran everything required by pol_payments_high_risk v4." (absence of deferral is information, not blank space).

## 3. GitHub surface UX (contract for github-connector + GITHUB_APP_PLAN)

### 3.1 The Agent Verification Gate check
One check per candidate/head-SHA. Never multiple parallel Sauron checks (dedupe on `(head_sha)`; repair re-entry UPDATES the same check name).
- Title: `Agent Verification Gate` (stable string; required-check config friendly).
- Icon: neutral circle while running (evidence accumulating), standard success/failure on terminal. No emoji, no score inflation.
- Summary text template (check-run output heading):
```
{verb_phrase} · confidence {0.xx} · evidence {accepted}/{required} kinds · policy {policy_id} v{n}
```
verb_phrase ∈ "Eligible for merge train" | "Rejected" | "Deferred — {primary_reason_short}" | "Validating ({tier} {job})" while in-flight.
Body (markdown, mirrors dossier sections in compressed form): ✓ accepted kinds (one line each, with counts e.g. "44 selected tests, 1,842 skipped as non-evidence"); ○ deferred kinds WITH one-clause reason; ⚠ uncertainty bullets; post-merge obligations only when verb=eligible.
- In-progress updates throttled: update check on tier transitions and evidence accepts, max 1/min (avoid GitHub API rate burn).

### 3.2 Deep-link conventions
`details_url` = `{WEB_BASE}/candidates/{cand_id}?at=dec_{decision_id}&src=gh_check`. Web ignores unknown query params (zod-stripped); `src=gh_check` enables a "came from GitHub" context chip on the dossier (helps support/debug). No PII, no tokens in URL. For synthetic-intent PRs the check still links the candidate (candidate exists even when intent is synthetic).

### 3.3 PR comment strategy: argue for restraint
Default = **check-only**. Reasons: (a) agents open dozens of PRs; comment noise trains humans to ignore Sauron exactly when trust matters most; (b) the check summary already carries decision+confidence+counts — duplicating it halves information density per scroll-page; (c) comments create unmanaged mutable state outside the ledger (edits/deletes break auditability), violating "ledger is the only facts authority"; (d) required-check gating works identically without comments.
Exceptions where a comment earns its noise (W3+, opt-in per policy): (i) **conflicting relation detected** touching a human-authored PR ("your PR conflicts with agent candidate X — coordinate"); (ii) escalation-to-human packets too rich for check body (failure diagnosis + repro + repair envelope); (iii) supersede notices on PRs whose checks were cancelled ("superseded by representative cand_Y"). Each exception posts ONE comment per cause class per PR, edits itself thereafter, never threads.

### 3.4 Failure annotations format
Check annotations (max 10/head, GitHub limit respected), one per distinct FailureCase:
```
title: {classification} (confidence {0.xx})
message: {one-line causal signal}. Repro: `{reproduction_command}`. Routed: {routed_action}.
```
`functional_regression` example message: "Fails on candidate and merge simulation; passes on base and 2 reruns. Trace diverges after reserveCharge(). Repro: `bazel test //payments/checkout:retry_contract_test`. Routed: scoped repair (max 2 attempts)."
security_policy_violation never suggests repair wording — always "Blocked; routed to humans."

## 4. Trust & explainability principles

T1. **Every automated decision renders WHY**: decision banner always expands to `explanation.factors[]` (name/value/source triples straight from §7 record — no UI-invented rationale).
T2. **Render what was skipped and why**: `evidence_deferred[].reason` is mandatory in dossier schema; UI treats it as first-class (same visual weight as accepted list), never a footnote. Skipped ≠ passed (I-01) — skipped items get ○ glyphs, never ✓.
T3. **Uncertainty is content, not error styling**: `known_uncertainty[]` renders as neutral informational blocks with mitigation text; reserved for things a human would ask about.
T4. **Calibrated language rules** (binding for all copy):
- Confidence words: ≥0.95 "high", 0.80–0.94 "moderate", 0.50–0.79 "low", else "insufficient". Numbers always shown alongside words.
- Banned phrases: "guaranteed", "fully verified", "safe to merge", "all tests pass" (when selection occurred), "CI green". Allowed: "eligible under {policy}", "required evidence accepted", "{n} suites deferred with reasons".
- Verbs mirror ledger verbs exactly (eligible_for_merge_train renders as "Eligible for merge train", never "Approved").
- Negative space stated positively: "No reachable dependency path" not "tests missing".
T5. **Shadow-mode surfacing**: while autonomy=0, a persistent banner on dashboard + every dossier: "SHADOW — decision recorded locally, not published to GitHub." Trust metrics strip compares counterfactual vs actual: `would-have-{blocked|deferred|prioritized}` counts and agreement rate with real CI outcomes; drill-in lists the divergence cases (the interesting ones) linking each dossier. After autonomy ≥1, shadow comparisons move to a historical panel (still auditable).
T6. **Provenance always one click away**: every decision/evidence row links its ledger events; chain-verified badge reflects checkpoint verifier state; a failed verification is a global red banner (fail-closed UI mirrors fail-closed API).

## 5. Human-in-the-loop model

Gate map (from DOMAIN_MODEL_DRAFT §1/§3/§8; H = prompt appears):

| Transition | Condition | Human prompt location |
|---|---|---|
| validating → merge_ready | autonomy <4 OR risk ≥ high | queue + intent detail action bar |
| validating → repairing | autonomy <3 or class ∉ auto_repair_classes | queue (packet: envelope + repro) |
| blocked → validating (unblock override) | always H variant | intent detail |
| Tier-4 entry | autonomy <4 or risk high/critical | queue |
| critical-risk `human_approval` evidence kind | policy-required, always H | queue + intent detail |
| policy draft → active / retire | H per §1.10 | OUT of UI v0.2 (CLI/operator); Settings renders result |
| security_policy_violation routing | never auto-waived | queue, top-pinned, red |

Prompt anatomy (every H item): subject link → dossier/intent context embedded (no blind approvals), budget/cost visibility (est_cost_millicents of proceeding, remaining tenant budget), expiry/deadline if any, reason-required on both approve and reject (symmetric friction prevents rubber-stamping). Approve emits compensating ledger event citing `hd_id`; audit trail = the resolved-item thread + ledger tail filter `type=human_decision.*`.

Autonomy dial UX: Settings shows current level + next-level delta preview ("At level 4: low/medium merge_ready becomes automatic — projected N decisions/wk based on shadow stats"). Changing it stays out-of-band until policy API exists.

## 6. API gaps (contracts-ready shapes)

Existing CORE-v1 covers dossiers/board/events. Gaps, minimal:

| # | Endpoint | Shape sketch | Priority |
|---|---|---|---|
| G1 | `GET /v1/human-decisions/pending?limit&older_than` | `{items:[{hd_id, created_at, subject{intent_id,candidate_id?,fc_id?}, trigger:class\|gate\|escalation, reason_code, context_ref, deadline?}]}` | **CORE-v0.2** |
| G2 | `POST /v1/human-decisions/{id}:approve` / `:reject` / `:unblock` | `{reason:string(min 16 chars)} → 202 {hd_id, resolution, ledger_evt}` · errors 409 already_resolved/expired | **CORE-v0.2** |
| G3 | `GET /v1/installations/status` | `{installations:[{installation_id, account, repos:[{name, webhook_state, last_delivery_seq, last_event_at}], permissions{}}]}` | **CORE-v0.2** (connector W2 dependency) |
| G4 | `GET /v1/candidates/{id}/dossier?at=dec_{id}` | same §7 body but decision block pinned to requested Decision (else latest) | **CORE-v0.2** (cheap: read-side only, powers permalinks) |
| G5 | `GET /v1/policies/active` (+`?history=1`) | `{policies:[full §8 bodies + status + activated_by/at]}` | CORE-v0.2 (readonly trivial given projections) |
| G6 | `GET /v1/metrics/shadow?window=7d` | `{verdicts:{eligible,rejected,deferred}, agreement_rate, divergences:[{subject, ours, actual}], latency_p50_ms}` | LATER (W3; dashboard degrades gracefully) |
| G7 | `GET /v1/budgets` | `{tenant_hour:{cpu_minutes:{limit,consumed}}, concurrent_candidates:{...}, env_templates:{...}}` | LATER (derivable from leases today) |
| G8 | `POST /failure-cases/{id}/escalate` | already specced §5 #13 | LATER (W2 as planned) |

Rules: all responses zod-schema'd in `apps/web/src/lib/api-schemas.ts` fail-closed; G1/G2 require Idempotency-Key on mutations; uniform 404 tenancy per D11.

## 7. Information density rules

Color semantics (tokens, not raw hex):
- Risk pills: low=neutral, medium=blue, high=amber, critical=red — border-tinted, never fill-flooded.
- Verdict kinds: eligible=green outline, rejected=red outline, deferred=amber outline, in-flight=animated pulse. Outline-only everywhere: color encodes state, text encodes meaning (colorblind-safe by construction).
- Evidence glyphs: ✓ accepted · ○ deferred/skipped · ✕ failed · ⟳ running · ⏹ superseded/cancelled. Never reuse a glyph across meanings.
- Red is scarce: reserved for failures, security, stalled feeds, broken chains. Amber = attention-with-context. If everything is red, the palette is broken.

Progressive disclosure, three depths everywhere: **L0 summary** (card/banner: verdict + confidence + completeness%) → **L1 factors** (expandable: explanation.factors, deferred reasons, cost meters) → **L2 raw** (ledger event timeline, digest strings, policy JSON). Default depth = L0; dashboards never exceed L0+L1; L2 lives on detail pages. No modals hiding L2 — deep-linkable routes only.

Latency/live-update expectations (stated in UI, honored by code): event tail applied ≤2s end-to-end; optimistic UI only for human actions with rollback-on-error; stale-feed detector (>60s without seq advance) shows amber "live feed paused — showing snapshot at seq N"; timestamps relative with absolute on hover; ordering claims cite seq, never wall clock (EC-031).

Density calibration: tables target 32–40px rows, monospace IDs truncated middle (`int_…f8`) with copy-on-click; numbers right-aligned tabular; zero decorative imagery. Empty ≠ sparse: teaching empty states carry the same grid discipline.

## 8. Wave breakdown (file-level, sized)

Assumes ARCHITECTURE wave numbering; UX waves ride behind their backend deps. All tasks include zod boundary schemas + fail-closed handling + vitest coverage per existing patterns.

**U-W1 — Trust core on existing APIs (no backend deps)**
- [S] `apps/web/src/components/decision-banner.tsx` — extract from dossier-view; add factor-list expander (L0→L1), calibrated-language helper `lib/calibrated-copy.ts`.
- [S] `apps/web/src/lib/calibrated-copy.ts` + tests — confidence-word mapping, banned-phrase lint fixture.
- [M] `apps/web/src/app/candidates/[id]/page.tsx` — permalink hardening: `?at=dec_*` param parse (graceful no-op until G4), copy-evidence-link button, superseded/invalidated banners.
- [M] `apps/web/src/components/shadow-banner.tsx`, `board-summary-strip.tsx` — shadow mode banner + placeholder trust strip (hidden until G6).
- [S] Teaching empty-states: upgrade `empty-state.tsx` to 3-line contract; apply to board/clusters/dossier-deferred.

**U-W2 — Human-in-the-loop (needs G1–G2)**
- [L] `apps/web/src/app/decisions/page.tsx` + `components/decision-queue.tsx` — queue list, aging, inline approve/reject/unblock with reason modal, idempotency-key reuse of `lib/idempotency-key.ts`, 409 handling.
- [M] `apps/web/src/app/intents/[id]/page.tsx` — human action bar (H-gate visibility logic in `lib/human-gates.ts`), escalation thread embed.
- [M] `apps/web/src/lib/api-schemas.ts` + `sauron-api.ts` — G1/G2/G4/G5 schemas + client methods.
- [S] `apps/web/src/app/settings/page.tsx` — readonly policy renderer (`components/policy-view.tsx`), §8 JSON → tables.

**U-W3 — GitHub surface + onboarding (needs connector + G3)**
- [M] `services/github-connector/internal/render/check.go` — check title/summary/body templates per §3.1 (pure functions, table-tested); annotation formatter per §3.4; throttle logic.
- [M] `apps/web/src/app/onboarding/page.tsx` + `components/connect-wizard.tsx` — 3-step wizard, disclosure panel, callback handling.
- [M] `apps/web/src/app/installations/page.tsx` — status table, stalled detection, resync affordance.
- [S] `apps/web/src/components/context-chip.tsx` — `src=gh_check` chip on dossier.

**U-W4 — Manager layer (needs G6/G7, otherwise stubs stay hidden)**
- [M] `apps/web/src/components/budget-strip.tsx`, `metrics/shadow-panel.tsx` — budget meters, autonomy panel, divergence drill-in list.
- [S] `apps/web/src/app/page.tsx` — group-by risk toggle, filters persisted to query params.
- [S] `docs/plans/PRODUCT_UX_PLAN.md` — keep in sync as gates ship.

## 9. Open questions for product decisions

1. **Who may click approve?** v0.2 has admin-token auth only; do we ship queue actions gated on a single admin scope, or hold U-W2 UI read-only until RBAC exists? (Blocks G2 exposure.)
2. **Permalink immutability vs re-renders**: after evidence invalidation, should `?at=dec_X` views freeze fully (snapshot copy) or re-render live with a "state changed since" banner? Snapshot is more trustworthy, costs storage.
3. **Synthetic-intent adoption flow**: self-serve "adopt this PR's synthetic intent" button (new POST endpoint + ownership proof) vs CLI-only forever? Affects developer journey step 4.
4. **Comment exceptions timing**: ship conflicting-relation PR comments in W3 with connector, or defer all comments until opt-in policy UI exists?
5. **Shadow metrics definition sign-off**: agreement_rate denominator (all decisions? only divergences?) and whether "would-have-blocked" counts need ground-truth labels from full-suite runs — needs TEST_STRATEGY owner (mirrors DOMAIN_MODEL §9.8).
6. **Queue SLA surface**: should unresolved-queue-age alarm anyone (email/slack) in v0.2, or purely passive dashboard?
7. **Onboarding posture choice**: v0.2 shows compiled-in policy readonly — do we fake multi-posture selection (cosmetic) or show truth (single default)? Truth recommended; confirm no demo-value objection.


---

## 10. INTEGRATOR RULINGS (FROZEN for W5)

1. Human approvals: G1/G2 ship gated on existing ADMIN bearer scope; UI shows action bar only for admin-scoped sessions, read-only otherwise. RBAC = v0.3.
2. `?at=dec_X` permalinks: re-render live data with a "state changed since this decision" banner in v0.2; snapshot copies = LATER.
3. Synthetic-intent adoption: API/CLI-only in v0.2; dossier shows amber synthetic flag, no adopt button.
4. PR comments: NONE in v0.2 — check-only (restraint argument adopted wholesale); exceptions revisit at W3+ behind policy opt-in.
5. Shadow metrics (G6): LATER; strips render hidden until endpoint exists (graceful absence contract).
6. Queue SLA alarms: passive dashboard only in v0.2.
7. Onboarding posture: show TRUTH (single compiled-in default policy readonly) — no cosmetic choice screens.
