# GitHub App Integration Plan — v0.2 "Connect GitHub end-to-end"

Status: PLAN (planning wave) · Owner: GitHub App Integration Architect · Execution target: W5
Inputs: `CI_CD and build systems Most pipelines were design.md` (§ Installation flow, permission
table, webhook events, Agent Integration API), `docs/ARCHITECTURE.md` (FROZEN v1),
`packages/contracts/internal-protocols.md` §1/§4, `docs/plans/ARCHITECTURE_DRAFT.md` §2.4,
`docs/SPEC.md` §3, THREAT_MODEL T6/B2, EDGE_CASES EC-001..010.
Normative docs win wherever this plan drifts; deltas below are *proposals* to the integrator.

## 0. Where v0.2 starts (current facts)

- **ingest :8080** serves bare `POST /hooks/github` (+ `/v1/hooks/github` alias): constant-time
  HMAC-SHA256 verify on `X-Hub-Signature-256`, GUID dedupe via partial unique index over `sig_ok`
  rows, rejected-signature quarantine (EC-003), 25 MiB cap, retry/backoff forwarding to
  control-plane `POST /internal/ctrl/deliveries` with `X-Sauron-Signature` +
  `Idempotency-Key: <ext_delivery_id>`. `X-Sauron-Timestamp` skew check (±5 min, EC-004) exists
  but is OPTIONAL and GitHub never sends it (see §6).
- **control-plane :8081** appends `delivery.accepted` to the hash-chained ledger per delivery,
  idempotent by ext_delivery_id via `command_log`; agent REST API (intents→leases→candidates→
  dossier→events tail); synthetic intents auto-created from PR webhooks (D12).
- **github-connector :8083** consumes HMAC-pushed `decision.rendered` envelopes
  (`POST /internal/connector/decisions`), renders ONE completed "Agent Verification Gate"
  check-run payload (`checks/render.go`: verb→success/failure/neutral, unknown fails closed);
  `DryRunPublisher` (logs payload) vs `LivePublisher` (go-github/v66 `CreateCheckRun`);
  stdlib-RS256 `InstallationTokenSource` (App JWT iat−60s/exp+10m →
  `POST /app/installations/{id}/access_tokens`, cached until expiry−60s) already implemented.
  Config is SINGLE-installation (`APP_ID`+`KEY_FILE`+`INSTALLATION_ID` all-or-dry-run).
  `ghconn.installations(id, account_login, created_at)` + `check_reports` exist.
- **apps/web :3000** derives board from ledger tail; dossiers at `/candidates/[id]`.

Gap to "end-to-end": no real app registration, no inbound installation lifecycle, single hard-coded
installation, completed-only check (no queued/in_progress), no synchronize→revision chain, no
re-run handling, no rotation story. v0.2 closes these.

---

## 1. App registration & lifecycle

### 1.1 Creation path

**Decision: manual registration for v0.2 (documented 10-minute runbook), manifest-based creation
as a scripted convenience, not a dependency.** Rationale: the webhook URL changes between dev
sessions (tunnels) and manifests pin the callback URL at creation; editing it afterwards requires
the settings UI anyway. The manifest flow (`https://github.com/settings/apps/new` with a manifest
JSON → user consent → `POST /app-manifests/{code}/conversions` returning `id`, `pem`,
`webhook_secret`) is delivered as `scripts/github-app-manifest.sh` that emits `.env.local`
fragments — useful for CI-owned demo apps and the eventual multi-tenant SaaS flow (v0.3+), where
manifest-onboarding becomes the product surface.

Runbook (manual): create App → set webhook URL → generate + download PEM → set webhook secret →
install onto ≥1 repo → record App ID, Installation ID, key path in `.env.local`.

### 1.2 Permission set (minimal, justified)

| Permission | Access | Why | Used by |
|---|---|---|---|
| Metadata | Read | Mandatory for all Apps; resolves repo owner/name, installation binding | every API call |
| Checks | Read & write | THE write surface: create/update "Agent Verification Gate"; also gates receiving `check_run` events (re-runs, §4.5) | connector |
| Pull requests | Read | Map PR events→intents/candidates; head/base SHA, branch, author login, PR number correlation | control-plane normalizer |
| Contents | Read | Fetch diffs/files for evidence provenance + discover `.integration/agent-control.yaml` adapter in v0.3 without forcing a second customer consent cycle (permission escalation triggers re-approval banners on all installs) | connector (v0.3 consumers) |

Deliberately NOT requested (the trust story — shown verbatim in the README/onboarding):

| Denied permission | Trust rationale |
|---|---|
| Contents: Write | We never push branches/commits/files. A verification authority that can author code destroys the evidence chain it certifies. |
| Pull requests: Write | We never merge, close, label, comment-as, or assign. Merge authority stays human/branch-protection until L4 autonomy (design doc levels), and even then via a separately consented grant. |
| Workflows (any) | Workflow YAML write ≈ arbitrary code execution in customer CI. Absolute deny in v0.x. |
| Administration | Would let us mutate branch protection ourselves. Admins configure the required check manually (§3.4) — deliberate friction that keeps us out of their security posture. |
| Commit statuses RW | Legacy surface; Checks API supersedes it for our gate. Avoids double-status noise. |
| Actions / Deployments / Members / Org | Observe-mode telemetry (Actions durations/flakes) is valuable but not needed for v0.2's connect-the-loop goal; each added read widens blast radius of a leaked token. Deferred to the observe-mode wave. |

Net: a leaked v0.2 credential can post check runs and read metadata/PRs/files of installed repos —
nothing else. Say exactly that in onboarding.

### 1.3 Event subscriptions

Subscribe (GitHub App "Subscribe to events"): `pull_request`, `push`, `installation`,
`check_run` (added beyond the original shortlist — REQUIRED for re-run semantics §4.5;
flagged in §9 for sign-off). Content types: `application/json`; SSL verification on; active.

Consumed actions in v0.2: `pull_request.opened|reopened|synchronize|closed`,
`push` (any branch; filtered by kind, §3.2), `installation.created|deleted|
new_permissions_accepted`, `check_run.rerequested` (others ack+count only, EC-007 posture).

### 1.4 Webhook URL & secret rotation

- **URL:** `https://<public-host>/hooks/github` (bare path per SPEC §3 convention). Dev:
  tunnel (§5.4). Prod: reverse proxy → ingest replicas; GitHub retries on non-2xx, so 503 +
  `Retry-After` during deploys is correct-by-contract.
- **Rotation (runbook, EC-010):**
  1. Generate new secret; set `SAURON_INGEST_WEBHOOK_SECRETS="new,old"` (ordered; first = new).
     Ingest tries each constant-time; any match passes. Overlap window ≤ 24 h (alert if exceeded).
  2. `PATCH /app/hook/config` (App JWT auth) or App settings UI: set the new secret.
  3. Flip env to `"new"` alone; remove old after window. Audit-note rotation in ledger via
     `security.rotation_recorded`-shaped delivery row? No — rotations land in ops log only; ledger
     stays domain-events (matches SECURITY_TRUST_DRAFT audit-grade list: record the *failures*,
     not the ceremony).
- Connector-side secret (`SAURON_CONN_WEBHOOK_SECRET`) is internal-to-sauron (ctrl→connector);
  rotate independently with the same dual-accept trick if ever needed.

---

## 2. Identity & token flows

### 2.1 Installation tokens

Flow (already implemented in `internal/ghauth/installation.go`, kept as-is):
stdlib RS256 App JWT (`iss=<app id>`, `iat=now−60s` clock-skew guard, `exp=now+10m`) →
`POST /app/installations/{id}/access_tokens` → token valid ≤ 1 h, cached in-source until
expiry−60 s, mutex-guarded, single-flight.

v0.2 deltas:
- **Per-repo scoping at mint time:** exchange body becomes
  `{"repositories":["<name>"],"permissions":{"checks":"write"}}` instead of `{}`. Today `{}`
  yields an ALL-repositories token; per-repo narrowing means a bug in one publish path can never
  touch another repo. Cost: one extra mint per (installation, repo) per hour — trivial vs budget (§4.6).
- **Key custody:** dev = PEM file mount (`platform/dev-keys/github-app.dev.pem`, gitignored,
  chmod 600). Prod v0.2 = same file model behind secret management of the deploy target;
  v0.3+ = KMS-backed signer behind a `ghauth.Signer` seam (the JWT-mint function is the only
  caller of `privateKey.Sign` — extract interface then; do NOT build KMS support now).
- **Redaction:** connector gains the same fail-closed scrubber discipline as ingest: never log
  `Authorization` headers, tokens, JWTs, or key material; dry-run payload logs contain no secrets
  by construction (they're check payloads).

Hard rule (B2, restated): installation tokens terminate at the connector. Never forwarded to
control-plane payloads, fleet job specs, or sandboxes (ARCHITECTURE §5.3 already forbids the latter).

### 2.2 User-level identity mapping WITHOUT OAuth (v0.2 boundary)

What we CAN do with webhook payloads + app tokens only:

| Capability | Mechanism | Where it surfaces |
|---|---|---|
| Attribute a PR/push to a GitHub login | `payload.sender.login`, `pull_request.user.login` | ledger actor refs `github:<login>` (kind=`github`); synthetic-intent metadata; dossier "opened by" |
| Correlate agent-authored PRs | branch naming convention `agent/<intent_id>/…` (Agent Integration API already suggests `agent/int_94f8/candidate_01`) | candidate↔intent linkage without any user auth |
| Show reviewer state | PR payload fields (review comments count etc., read-only view) | dossier context |

What we CANNOT do in v0.2 (say this in docs): verify that a login corresponds to a real Sauron
user; obtain user consent; act AS a user (approve reviews, comment, merge); read org membership
beyond payload hints; bind a browser dashboard session to a GitHub identity. All actor strings are
**display-only provenance**, never authorization inputs (tenant predicates derive from the admin
token / installation binding, D11).

**v0.3 OAuth upgrade path (designed-for, not built):** enable the App's "Request user authorization
(OAuth) during installation" + identify scope (`read:user`). Callback → exchange code for
user-to-server token → `ctrl.identities(github_login PK, sauron_principal, linked_at)` table;
dashboard "Sign in with GitHub" links principals; user tokens unlock acting-as-user features
(comments, approvals) behind explicit per-feature policy flags. Agents remain server-identity only.
No schema in v0.2 blocks this: ledger actor refs already carry `github:` prefix.

---

## 3. Webhook pipeline wiring

### 3.1 Event → normalized_kind mapping (ingest passes `X-GitHub-Event[.action]` verbatim;
control-plane normalizer projects effects)

| GitHub delivery | normalized_kind | Ledger effect(s) | Notes |
|---|---|---|---|
| `pull_request.opened` / `.reopened` | `pr.opened` | synthetic `intent.declared` (D12) + `candidate.submitted(head_sha, base_sha, patch_ref=diff_url)` | reopen reuses existing intent if projection finds one (idempotent) |
| `pull_request.synchronize` | `pr.synchronize` | NEW candidate revision (same intent) + `candidate.superseded(prior, reason=head_advanced)` + `validation.cancelled`(queued) + `evidence.invalidated(inputs_hash changed)` | see §3.3 |
| `pull_request.closed` | `pr.closed` | `candidate.cancelled(reason=merged\|closed_without_merge)` | late pushes after close create NO work (EC-002) |
| `push` to tracked base branch (default branch / configured) | `push.base_advanced` | `merge_base.advanced(new_base_sha)` → existing invalidation cascade (batch, staggered — EC-026) | branch set from installation config; default = repo default_branch |
| `push` to any other branch | `push.branch` | `delivery.accepted` only (record-only) | agent branches get work via PR events or API, not raw pushes |
| `installation.created` | `installation.created` | ghconn row insert + audit | §5.3 |
| `installation.deleted` | `installation.deleted` | suspend flow (§6.4) | |
| `installation.new_permissions_accepted` | `installation.permissions_changed` | audit row, permissions snapshot | rotation/escalation visibility |
| `check_run.rerequested` (ours, by `external_id`) | `check_run.rerequested` | re-validation command (§4.5) | |
| anything else | `unknown.<event>.<action>` | persist + ack + `unknown_event_total{kind}` counter (EC-007 forward-compat park) | never 4xx — GitHub would disable delivery |

Unknown/unparseable-but-signed payloads already persist raw (SPEC §3 migration 0003) — keep that.

### 3.2 Idempotency

Unchanged contract: `X-GitHub-Delivery` GUID is the sole external identity; `(source, ext_delivery_id)`
partial-unique dedupe + in-flight map handles redelivery storms (EC-001/011). New requirement:
normalizer effects keyed by `(ext_delivery_id)` inside the effect tx (`processed_events` guard,
I-12) so at-least-once forwarding never double-submits candidates. synchronize storms: GitHub may
deliver synchronize faster than projections update — normalizer must tolerate out-of-order
head_shas by comparing against the candidate projection's current head and ignoring regressions
(stale delivery ⇒ record-only diagnostic, no supersede of newer work).

### 3.3 synchronize → candidate revisions + evidence invalidation

PR #N maps to the synthetic/declared intent via new projection index `(repo, pr_number) → live
candidate`. On synchronize: submit candidate with new head_sha. Existing duplicate_sha semantics
(migration 0006: unique `(intent_id, head_sha, base_sha)` WHERE live) give us revision identity
free — same head re-delivery is a 200 replay; genuinely new head = fresh candidate with fresh
`inputs_hash` (base SHA, lockfiles, toolchain per I-02). Prior candidate: `superseded(by=revision,
reason=head_advanced)`; its queued validations cancel; accepted evidence stays ATTRIBUTED but
`evidence.invalidated(causation=synchronize delivery)` — never deleted (ledger immutability), and
cache/reuse keys miss because inputs_hash changed. Repair loops mid-flight die with the revision;
the repair envelope re-opens against the new candidate if policy warrants.

### 3.4 Branch protection integration (Agent Verification Gate as required check)

Strategy: **manual admin configuration, zero API assistance in v0.2** (Administration deliberately
unrequested, §1.2). Runbook steps: Settings → Branches → main → Require pull request before
merging → require status check `Agent Verification Gate` (exact const from `checks/render.go`;
name is a compatibility contract — NEVER rename without a migration note). Recommended extras:
require branches up-to-date OFF initially (our `merge_base.advanced` invalidation already enforces
freshness more surgically; strict mode forces rebases that fight the scheduler). Because required
checks only block once the check EXISTS on a SHA, the queued-phase check (§4.2) matters: it makes
the gate visible the moment a candidate registers, not only at decision time.

---

## 4. Check-writer design

### 4.1 Lifecycle: completed-only → three-phase

Today `Render()` emits one completed check. v0.2 generalizes the ctrl→connector push
(internal-protocols §4 delta PROPOSAL — integrator folds):

```json
{ "kind": "lifecycle",           // discriminates from today's decision envelopes
  "phase": "queued|in_progress",
  "candidate_id": "cand_…", "repo": "acme/payments", "head_sha": "…", }
```

Emitted from existing outbox events (`candidate.submitted` → queued;
first `validation.started` per candidate → in_progress). Connector keeps a
`check_reports(check_run_id)` per (candidate, phase-transition) and UPDATES the same check run
(`Checks.UpdateCheckRun`) rather than stacking new ones: one check run per candidate revision,
status walking `queued → in_progress → completed`.

### 4.2 Status/conclusion mapping (extends `render.go`, fail-closed preserved)

| Phase | status | conclusion | trigger |
|---|---|---|---|
| queued | queued | — | candidate registered |
| in_progress | in_progress | — | first validation started |
| completed | completed | success ← `eligible_for_merge_train`; failure ← `rejected`; neutral ← `deferred` | `decision.rendered` |

Timed-out/stale candidates eventually surface via existing reconciler → decision verbs; connector
adds a safety net: check runs stuck non-completed > configurable age get flipped to `neutral`
with "stalled" summary (prevents eternal yellow under required-check protection).

### 4.3 Output summary format (markdown, deterministic, golden-tested)

```
**Eligible for merge train** · confidence 0.94 · policy pol_sauron_default v1
Evidence: 5/5 required accepted · 2 deferred (reason-linked) · 0 failed
→ Full dossier: {details_url}
_decision dec_01J… · candidate cand_01J… · rendered 2026-08-23T03:41:00Z_
```

Envelope extension (contract delta): add `evidence {required, accepted, deferred, failed}` counts
so the connector doesn't scrape projections. Deep link: `details_url =
{SAURON_CONN_DETAILS_URL}/candidates/{candidate_id}` — the EXISTING dossier page; in dev this is
localhost (unreachable from github.com — acceptable, noted in runbook).

### 4.4 Annotations

Failed required kinds → one `failure` annotation each, from failure-case data pushed in the
decision envelope (`annotations: [{path, start_line?, message, kind}]`, capped 50/batch — GitHub
hard limit; overflow truncates with a final "N more in dossier" annotation). Missing path data ⇒
annotation without path is allowed (file-level message). Annotations only on `failure` conclusions.

### 4.5 Re-run semantics

`external_id` switches from decision_id → **candidate_id** (render.go change) so `check_run.
rerequested` maps back to the revision regardless of which decision it carries. Handler:

- Policy knob `SAURON_CONN_RERUN_POLICY ∈ {replan, replay_cached}` (default **replan**):
  - `replan`: connector → control-plane `POST /v1/candidates/{id}/revalidate` (NEW thin command,
    admin-auth'd internal route) appending a re-plan command → fresh plan under CURRENT policy +
    current inputs_hash; check flips back to `queued`. Honest re-validation; burns real budget.
  - `replay_cached`: re-publishes last decision unchanged (zero compute, summary says "cached").
- **Cost guardrails:** per-candidate rerun cap (default 2) + per-installation rerun rate
  (default 20/h) in the connector limiter; over-cap ⇒ check updated to `neutral` with
  "re-run budget exhausted — see dossier" (never silently ignored; a required check that ignores
  re-runs strands mergers).

### 4.6 Rate-limit budget per installation

GitHub Apps REST: ~1,500 core req/h per installation. Our steady-state write cost ≈ 3 check calls
per candidate revision + reruns. Connector owns a token-bucket limiter (pattern-match
`rate_limits` approach; connector-local, no Redis): default 300 write-calls/h/installation
(≈100 revisions/h ceiling), plus honoring `Retry-After` / secondary-rate headers with exp
backoff, and jittered retry on 5xx. Reads (future diff fetches) get a separate smaller bucket.
Budget exhaustion ⇒ queue internally (outbox-style), never drop a decision silently — a dropped
required check blocks merges invisibly.

---

## 5. Config & deployment deltas

### 5.1 New environment variables

| Var | Service | Purpose |
|---|---|---|
| `SAURON_INGEST_WEBHOOK_SECRETS` | ingest | ordered comma list, enables EC-010 rotation (falls back to singular var) |
| `SAURON_CONN_GITHUB_APP_ID` / `_PRIVATE_KEY_FILE` / `_INSTALLATION_ID` | connector | existing; INSTALLATION_ID becomes optional DEFAULT install for dev |
| `SAURON_CONN_RERUN_POLICY`, `SAURON_CONN_RERUN_MAX_PER_CANDIDATE`, `SAURON_CONN_RERUN_RATE_PER_HOUR` | connector | §4.5 |
| `SAURON_CONN_WRITE_BUDGET_PER_HOUR`, `SAURON_CONN_STALLED_CHECK_AGE` | connector | §4.6 / §4.2 safety net |
| `SAURON_CTRL_TRACKED_BASE_BRANCHES` | control-plane | which push refs count as base advances (comma globs, default `main,master`) |

### 5.2 Secrets layout

`platform/dev-keys/` gains `github-app.dev.pem` (gitignore BEFORE first commit; Makefile target
`make github-key-check` warns if missing when connector creds set). Prod: file-mounted secret via
deploy platform; KMS deferred (§2.1). Webhook secrets stay env-vars like today.

### 5.3 Compose additions

- `--profile github` services: **`webhook-forwarder`** (cloudflared container, `tunnel --url
  http://ingest:8080`, prints trycloudflare URL at boot) — zero-account local loopback; smee
  equivalent documented as alternative. Base profile untouched: dry-run connector stays the
  default dev posture (config.go all-or-nothing logic preserved).
- Connector env in compose grows the optional App-cred trio commented out (mirrors .env.example).

### 5.4 Public URL requirements (dev vs prod)

Dev: cloudflared quick-tunnel or `ngrok http 8080` (free static domain recommended so the App's
webhook URL survives restarts). Constraints: tunnels terminate TLS externally; HMAC verification is
unaffected; do NOT expose control-plane/connector publicly — only ingest needs ingress.
Prod: reverse proxy (TLS, HTTP/2, ≤25 MiB body, 10 s timeouts) → ingest replicas; optional
GitHub published-IP allowlist as defense-in-depth (HMAC remains the primary control); health
probe path exempt from rate limiting so GitHub's delivery-health monitoring works.

### 5.5 github-connector main.go architecture changes

1. `InstallationTokenSource` instances become a map `map[int64]*InstallationTokenSource` guarded
   by RWMutex (one per known installation), built lazily on first use; config's
   `INSTALLATION_ID` seeds the default entry (dev convenience).
2. Publisher resolution per envelope: resolve repo→installation (below) → live publisher w/
   that source; unresolvable/no-creds → `DryRunPublisher` with reason logged. Dry-run default
   for local dev UNCHANGED.
3. Repo→installation resolution: `ghconn.installations` joined against a maintained
   `installation_repos(owner, repo, installation_id)` cache fed by (a) lazy probe on first sight
   of a repo (GET /repos/{o}/{r}/installation, 404⇒unknown), (b) installation events. Fail-closed:
   unknown repo ⇒ dry-run + metric, never guess an installation (cross-post risk, §6.3).
4. New handlers: lifecycle envelopes, rerequested, stalled-check sweeper goroutine.

### 5.6 Migrations (each service owns its own)

- `ghconn 0002`: `installations` + `suspended boolean`, `permissions jsonb`, `account_login`
  fill, `updated_at`; new `installation_repos` table; `check_reports` + `check_run_id` uniqueness
  per (candidate_id, head_sha) partial WHERE live.
- `ctrl 0009`-ish: `(repo, pr_number)` projection index on candidates/intents for synchronize
  lookup; `rerun_count` on candidates.

---

## 6. Security analysis

### 6.1 Webhook forgery/replay posture (what P1-1/T6 means HERE)

- **Forgery:** impossible without the webhook secret; constant-time compare; failures quarantined
  as audit rows (sig_ok=false, excluded from dedupe index so later-valid redeliveries still land).
- **Replay:** TWO layers. (a) Exact replay of a captured delivery: blocked INDEFINITELY by the
  `(source, ext_delivery_id)` unique constraint on sig_ok rows — stronger than any time window.
  (b) Near-replay (attacker re-signs modified content): impossible without the secret.
- **The P1-1 gap, stated honestly:** the ±5-min timestamp window (EC-004) only binds senders that
  emit `X-Sauron-Timestamp` — i.e., OUR internal forwarder hop. GitHub sends no such header, so
  for real GitHub traffic the window is vacuous — FINE given (a)+(b). Residual risk: a captured
  delivery replayed before the original ever arrives (ingest outage window) — GUID not reliably
  time-orderable, no mitigation available; accepted, documented, bounded impact (a duplicate
  candidate revision is absorbed by dedupe-by-head_sha).
- **Rotation** (§1.4) bounds secret-compromise blast radius; failure-burst lockout per source
  (EC-003) rides the existing rejected-row counter — add alert threshold.

### 6.2 Token scoping rules

Installation tokens: ≤1 h (GitHub-enforced), per-repo narrowed at mint (§2.1), permissions floor =
app permissions minus nothing (can't elevate), terminated at connector (B2). App JWT lives ≤10 min,
never persisted (memory only). No PATs anywhere (spec secret-model compliance).

### 6.3 Cross-installation isolation

Tenant Acme's repo must never be touched using tenant Beta's installation: enforced by
repo→installation resolution FAILING CLOSED (§5.5.3) before any mint; per-repo token scoping
(§2.1) as belt-and-suspenders; GitHub itself 404s cross-installation writes (third layer).
Ledger rows keep `repo` + installation id in delivery payload for forensic tracing.

### 6.4 Suspension / uninstall

`installation.deleted` (or GitHub-side suspension) ⇒: mark `installations.suspended=true` →
connector stops ALL writes for those repos instantly (dry-run logging continues) → control-plane
revokes active leases for that installation's repos (`lease.revoked`, cascades cancel queued
validations via existing propagation) → synthetic-intent creation halts for those repos. Data:
ledger is INSERT-only and RETAINED (tamper-evidence is the product); deliveries roll off at 30d;
check_reports retained (audit); GitHub preserves historical check runs on their side. Re-install
creates a NEW installation id ⇒ new row; no resurrection of old bindings. Suspended-state
deliveries: accepted+recorded (audit continuity), zero effects.

---

## 7. Test strategy

1. **Recorded-fixture replay suite** (`tests/fixtures/github/*.json`, sanitized captures):
   every §1.3 event/action × happy/pathological variants (missing fields, fork PRs, renamed
   owners). Harness signs fixtures with a test secret and POSTs straight to ingest — no tunnel
   needed in CI; asserts ledger shapes, dedupe (EC-001/004/007), normalizer effects (§3.1 table
   becomes executable expectations).
2. **Local live loopback:** `--profile github` cloudflared/smee container → real GitHub App on a
   scratch repo → ingest. Manual smoke runbook + optional nightly job. Free-tier caveats
   documented (URL rot, bandwidth).
3. **Golden check-run payloads:** extend `render_test.go` goldens to every phase × verb ×
   annotation shape; dry-run log lines asserted byte-stable (existing pattern); protects the
   required-check-name compatibility contract.
4. **Contract tests vs go-github/v66:** decode fixtures into `github.PullRequestEvent`,
   `PushEvent`, `InstallationEvent`, `CheckRunEvent`, assert every field path the normalizer /
   rerun handler reads survives library bumps; pin v66 in go.mod (charter-approved dep).
5. **Fake-GitHub e2e:** `httptest.Server` implementing `POST /repos/{}/check-runs` +
   installation-token endpoint; full loop: fixture → ingest → ctrl → sim validations → decision →
   LIVE publisher → assert queued→in_progress→completed call sequence, per-repo token scoping
   claims, rate-limiter behavior (force 429/Retry-After), rerun policy both modes, suspension.
6. **Property tests:** synchronize/out-of-order delivery streams converge to one live candidate
   per PR (rapid), mirroring storm posture (EC-002/011/026 ride-alongs).

---

## 8. Implementation waves (ordered, builder-ready; sizes S≤½d, M≈1d, L≥2d)

| # | Task | Size | Touchpoints | Depends on |
|---|---|---|---|---|
| G1 | Dual-secret rotation window in ingest (EC-010) | S | `services/ingest/internal/config/config.go`, `api/signature.go` + tests | — |
| G2 | installations migration + repo→installation resolution store | M | `services/github-connector/migrations/0002_*`, `internal/store/{pg,store}.go` | — |
| G3 | Normalizer expansion: mapping table, unknown-event park, out-of-order guard | M | `services/control-plane/internal/api/delivery_handler.go`, projection reducer + store migration (pr_number index) | — |
| G4 | synchronize→revision chain: supersede + evidence.invalidation causation | M | ctrl projection/evidence packages, `domain/intent.go` | G3 |
| G5 | Per-installation token sources + per-repo mint scoping | M | `internal/ghauth/installation.go`, `cmd/github-connector/main.go` | G2 |
| G6 | external_id=candidate_id + evidence-count envelope fields + summary format | S | `internal/checks/render.go`, contracts §4 proposal | — |
| G7 | Lifecycle phases (queued/in_progress) via widened §4 envelopes + update-in-place | M | ctrl relay push, connector `internal/api/decisions_handler.go`, `internal/checks/*` | G5, G6 |
| G8 | Annotations for failed required kinds (50-cap batching) | S | `internal/checks/render.go` + goldens | G6 |
| G9 | Rerun handler + policy knobs + budget caps + `/v1/candidates/{id}/revalidate` | M | connector `internal/api/`, ctrl api + command plumbing | G7 |
| G10 | installation.created/deleted/permissions handling + suspend→lease revocation | M | connector api/store, ctrl lease revocation call | G2 |
| G11 | Stalled-check sweeper + write-budget limiter + Retry-After backoff | M | connector new `internal/ratelimit/`, sweeper in main | G7 |
| G12 | Fixture replay suite + fake-GitHub e2e (§7.1/7.5) | L | `tests/`, connector/ctrl test helpers | G3–G9 |
| G13 | Contract tests vs go-github types (§7.4) | S | connector tests | G3 |
| G14 | Manifest script + compose `--profile github` + runbooks (rotation, required-check setup, tunnel) | S | `scripts/`, `platform/docker-compose.yml`, `docs/RUNBOOK.md` | G1 |

Parallelization: G1‖G2‖G3‖G6 start day 1; G4→G7→G9 is the critical spine; G12 tracks the spine
and gates wave exit with `make hygiene` + integration suites green (ARCHITECTURE §7).

Exit criteria: real App installed on a scratch org; PR open → queued check visible under branch
protection → decision lands → gate goes green/red/neutral with dossier link; synchronize
supersedes cleanly; uninstall suspends writes within one delivery; fixture suite + fake-GitHub
e2e green in CI.

---

## 9. Open questions (need human/product decision before W5)

1. **`check_run` subscription** — approved? (Adds an event class beyond the original shortlist;
   required for §4.5 re-runs.)
2. **Rerun default** — `replan` (truthful, costs budget) vs `replay_cached` (free, possibly stale)?
   And who owns the rerun budget numbers?
3. **Contents:Read now vs v0.3** — bundle to avoid a second consent cycle, or stay maximally
   minimal this quarter?
4. **Tracked base branches** — hardcoded `main,master` default OK, or per-installation config UI
   needed sooner?
5. **Tunnel/account ownership** for the persistent dev webhook URL (ngrok static domain account?)
6. **Tenancy timing** — keep single-tenant demo posture (D11) through v0.2 even with multiple
   real installations landing, or stamp tenant_id per installation now (cheap insurance)?
7. **OAuth v0.3 scope floor** — is identify-only (`read:user`) the agreed minimum for the
   identity-linking upgrade, with acting-as-user features gated per-feature later?


---

## 10. INTEGRATOR RULINGS (FROZEN for W5)

1. `check_run` subscription: APPROVED (read-only event class, required for re-runs).
2. Rerun default: `replan`; caps 2/candidate, 20/h/installation (env-tunable; budget owned by platform admin).
3. `Contents:Read`: INCLUDE in v0.2 permission set (avoids second consent cycle; read-only).
4. Tracked base branches: env default `main,master`; per-installation config UI = LATER.
5. Dev tunnel: cloudflared quick-tunnel default (zero-account); ngrok static domain documented as option in runbook.
6. Tenancy: D11 single-tenant posture stands through v0.2; installation_id stamped on deliveries + ghconn rows for forensics.
7. OAuth: v0.3, floor `read:user`, acting-as-user features gated per-feature later.
