# Sauron Central Spec Index

Normative documents (single source of truth per concern):

| Concern | Document |
|---|---|
| System architecture, services, ownership, ports | docs/ARCHITECTURE.md |
| Executable invariants I-01..I-14 | docs/INVARIANTS.md |
| Security requirements & P0 threats | docs/THREAT_MODEL.md |
| REST contract | packages/contracts/openapi.yaml |
| Event envelope + payloads | packages/contracts/events.schema.json |
| Internal service protocols | packages/contracts/internal-protocols.md |
| Repo structure & naming | docs/REPO_STANDARDS.md |
| Engineering rules (file caps, TDD, types) | docs/ENGINEERING_CHARTER.md |
| Delivery phases & gates | docs/ROADMAP.md |

Rule: builders never edit normative docs unilaterally — propose changes in final
reports or append to §3 below; the integrator reconciles into the frozen docs.

## §2 Dependency ledger

See docs/ENGINEERING_CHARTER.md §2 (authoritative copy mirrored here on change).

## §3 Change-log (append-only, newest first)

Agents append one row per contract-affecting change with a summary snippet.
Integrator folds accepted changes into frozen docs and marks them folded.

| Date | Agent | Change summary | Folded into frozen docs? |
|---|---|---|---|
| 2026-08-24 | wave-4b-recon | Fleet claim self-registration (B2/I-04 follow-up): `POST /internal/fleet/jobs/claim` now works for ANY worker_id — the handler registers the claiming worker before claiming, and `PGStore.ClaimJobs` re-registers unknown/purged workers (`INSERT … ON CONFLICT DO NOTHING`, outside the claim tx). Previously an unregistered id (default `anonymous`) died on `execution_jobs.claimed_by` FK (500), and RequeueStale's liveness GC reaped live executor slots, permanently stranding claims. Registration semantics now match MemoryStore. | ⬜ proposed for internal-protocols §2 claim clause |
| 2026-08-24 | wave-4b-recon | Admission budgets for fresh tenants + real hourly windows (P0-3 completion): remaining budget is keyed by QUEUED tenants (a tenant with no counter row has consumed nothing ⇒ full ceiling), and `budget_counters` gained `window_started_at` (migration 0011) so usage is lazily zeroed when the UTC-hour bucket rolls over — in snapshot reads and atomically inside the reserve upsert. Before, ceilings were all-time quotas: fresh tenants were denied outright and one storm permanently exhausted a tenant (`tenant_cpu_budget_exhausted` forever). Σreservations−Σreleases==used conservation is unchanged within a window. | ⬜ proposed for ARCHITECTURE I-06/I-10 note + §8 per_tenant_hour semantics |
| 2026-08-24 | wave-4b-recon | Live-probe protocol alignment (no frozen assertion weakened): i03/i04/i11 present the job-lease credential on complete/heartbeat — missing/invalid token ⇒ typed 401 unauthorized BEFORE fence evaluation; valid-token-but-wrong-fence ⇒ 409 fence_mismatch; equal-epoch replay ⇒ 409 already_accepted. Probe jobs seed into private per-call pools so a shared pool's FIFO head can never hand back a stale row without its credential. | ⬜ proposed for INVARIANTS I-03/I-04/I-11 live-mode notes |
| 2026-08-23 | wave-3-closer | Decision freshness (I-08/I-11/D6): completion ingestion absorbs feed rows for already-terminal runs or decided candidates as logged diagnostics (marked processed, no effects); `eligible_for_merge_train` is now blocked while a plan-required run sits in failed/timed_out or the candidate is repairing — the verb reflects ALL completed evidence; failure router keeps owning defer/reject/repair. PG regressions: completions_pg_test.go. | ⬜ proposed for ARCHITECTURE D8 note + INVARIANTS I-08 wording |
| 2026-08-23 | wave-3-closer | Reconciler stale-dispatched-run threshold is configurable via `SAURON_CTRL_STALE_RUN_MAX_AGE` (default keeps documented 2×15-min posture); scheduler batch via `SAURON_CTRL_SCHED_BATCH` (default 8). Dev compose sets tick 200ms, reconcile 5s, stale age 300s, batch 24, sim workers 24, rate limit 600/min — harness-window sizing only, prod defaults unchanged. | ⬜ proposed for ARCHITECTURE §3 ops table |
| 2026-08-23 | wave-3-closer | Completion-feed replay pre-check: control-plane bulk-reads `processed_events` before the effect pipeline (advisory only); the authoritative I-12 dedupe inside the effect tx is unchanged. Removes O(feed) re-processing per tick on a contract-mandated replaying feed. | ✅ internal-protocols §4 replay clause unchanged |
| 2026-08-23 | wave-3-conformance | `CanonicalJSON` now recursively key-sorts (struct values like ConflictRef serialized in field order, breaking independent `payload_sha256` recomputation — I-07). Numbers preserved byte-exact via UseNumber. | ✅ events.schema.json description already implies canonical bytes |
| 2026-08-23 | wave-3-conformance | `ingest.deliveries.payload` jsonb → text (migration 0003): audit anchor stores RAW received bytes verbatim; malformed-but-signed deliveries (EC-005) persist instead of 503-ing on INSERT. Redaction stays pre-forward. | ⬜ proposed for internal-protocols §1 |
| 2026-08-23 | wave-3-conformance | Intent declaration reserves the FIRST candidate slot (`initial_candidate_id`, migration 0008): stamped into intent.declared + lease.granted payloads and consumed atomically by the first submission, so the §3b lifecycle trace is observable end-to-end from one candidate id. | ⬜ proposed for ARCHITECTURE_DRAFT §3b note |
| 2026-08-23 | wave-3-conformance | validation.started / validation.completed payloads now stamp `candidate_id` (schema allows extra correlation fields) so the documented per-candidate sequence is visible in the public tail (ARCHITECTURE_DRAFT §3a). | ⬜ proposed for events.schema.json CORE notes |
| 2026-08-23 | wave-3-conformance | Clustering v0 classify: when BOTH members declare no changed_symbols (v1 REST has none), identical full path sets + trigram ≥ τ classify duplicate_of — otherwise supersede propagation was unreachable via the API (EC-012). Symbol-declared behavior unchanged; conflicts_with precedence preserved for partial overlaps. | ⬜ proposed for DOMAIN_MODEL_DRAFT §2 note |
| 2026-08-23 | wave-3-conformance | Default tenant is now the schema-valid fixed literal `org_01ARZ3NDEKTSV4RRFFQ69G5FAV` (prefixedUlid; Crockford base32). `org_default` violated events.schema.json on every envelope. Set via `SAURON_CTRL_TENANT_ID` (control-plane config.DevTenant). | ⬜ proposed for ARCHITECTURE D11 note |
| 2026-08-23 | wave-3-conformance | `ctrl.command_log.response_body` jsonb → bytea (migration 0005): idempotent replays now return the ORIGINAL response bytes; jsonb round-trips renormalized key order/spacing and broke openapi's "replay returns identical body" (I-12). | ⬜ proposed note for openapi description |
| 2026-08-23 | wave-3-conformance | Candidate duplicate_sha keying now includes base_sha (app guard + migration 0006 partial unique index `(intent_id, head_sha, base_sha)` WHERE live): same head under a moved base is a changed input ⇒ fresh plan with distinct inputs_hash (I-02), not a 409. Identical head+base still 409 duplicate_sha. | ⬜ proposed for openapi Conflict details.reason wording |
| 2026-08-23 | wave-3-conformance | Ingest quarantines signature-invalid deliveries as audit rows (`status='rejected'`, `sig_ok=false`) instead of dropping them; dedup uniqueness is now a PARTIAL index over sig_ok rows only (migration 0002) so a valid redelivery of a previously rejected GUID is still admitted; rejected rows are never retried. | ⬜ proposed for internal-protocols §1 |
| 2026-08-23 | wave-3-conformance | Ingest serves GitHub webhooks at bare `POST /hooks/github` (GitHub-facing convention, used by live suites); `/v1/hooks/github` kept as versioned alias. | ⬜ proposed for internal-protocols §1 |
| 2026-08-23 | wave-3-conformance | Fleet claim registers the claiming worker inside the claim tx before the claim UPDATE: unknown external worker ids (e.g. "anonymous") previously violated `execution_jobs.claimed_by_fkey` → 500. | ✅ internal-protocols §2 unchanged (behavior now conforms) |
| 2026-08-23 | wave-3-conformance | Post-terminal lease renewal now returns typed `409 conflict_state` with `details.reason ∈ {expired_lease, revoked_lease}` per openapi Conflict envelope (was unmapped ErrPostTerminal → 503). | ✅ openapi already specified this shape |
| 2026-08-23 | wave-2-integration | Compiled-in policy registry now serves TWO packs: the §8 payments document plus a wildcard fallback `pol_sauron_default` v1 — without it every low/medium-risk intent (and any non-payments repo) failed closed at planning (I-09). Most-specific-wins resolution unchanged; §8 payments pack byte-identical. | ⬜ proposed for ARCHITECTURE D7 note |
| 2026-08-23 | wave-2-integration | `delivery.accepted` aggregate ids are now platform-minted `dlv_`-prefixed ULIDs; external GitHub GUID lives only in `payload.ext_delivery_id` (emitters fixed in control-plane). | ✅ (schema already allowed `dlv_`) |
| 2026-08-23 | wave-2-integration | Added internal-protocols §4: control-plane→github-connector decision push (`POST /internal/connector/decisions`, HMAC via `SAURON_CONN_WEBHOOK_SECRET`) and the fleet completion feed (`GET /internal/fleet/jobs/completed`). | ✅ internal-protocols.md §4 |
| 2026-08-23 | builder-1e | Invariant suites delivered as `iNN.spec.ts`; INVARIANTS.md test-column renamed to match. | ✅ INVARIANTS.md |
| 2026-08-23 | integrator | Charter ratified; SPEC index created. | n/a |
