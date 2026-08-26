# CISync Runbook

Operational procedures for running CISync end-to-end. Normative refs:
`docs/ARCHITECTURE.md`, `docs/plans/GITHUB_APP_PLAN.md` (§1 registration,
§1.4 rotation, §3.4 branch protection, §5.4 tunnels).

## 1. Local development stack

```bash
make keys          # generate dev Ed25519 keys (ledger checkpoints + job leases)
make up            # postgres + ingest(:8080) + control-plane(:8081)
                   # + runner-fleet(:8082) + github-connector(:8083, dry-run) + web(:3000)
make test          # unit + property (Go)
make hygiene       # charter gate: lint + structure + 250-line cap
```

Live verification (requires stack):
```bash
export CISYNC_API_URL=http://localhost:8081 CISYNC_E2E=1 \
  CISYNC_ADMIN_TOKEN=dev_admin_token_not_for_prod \
  CISYNC_WEBHOOK_SECRET=dev_webhook_secret_not_for_prod \
  CISYNC_FLEET_URL=http://localhost:8082
cd tests && pnpm exec vitest run          # 74 invariant/e2e suites
docker build -t cisync/loadgen -f tests/loadgen/Dockerfile tests/loadgen
docker run --rm --network cisync_default cisync/loadgen -concurrency 100 -units 100 -repos 4 -dupes 2
```

NOTE (macOS): always load-test from INSIDE the compose network (`--network
cisync_default`). Host port-forwarding collapses under per-port concurrency and
will falsely fail the system (see SPEC §3, W3 findings).

## 2. Connecting a real GitHub App (v0.2)

One-time (~10 min), manual registration per GITHUB_APP_PLAN §1:

1. Create the App: github.com/settings/apps/new
   - Webhook URL: `https://<public-host>/hooks/github` (dev: step 3 tunnel URL)
   - Webhook secret: generate; set `CISYNC_INGEST_WEBHOOK_SECRET`
   - Permissions (exactly these): Metadata R · Checks RW · Pull requests R · Contents R
   - Subscribe events: pull_request, push, installation, check_run
   - Download the PEM → `platform/dev-keys/github-app.dev.pem` (gitignored)
2. Install onto pilot repos; note Installation ID from the URL.
3. Dev webhook ingress — no account needed:
   ```bash
   docker compose --profile github up webhook-forwarder
   # copy the trycloudflare.com URL printed at boot into the App's webhook URL
   ```
   For a stable dev URL use an ngrok static domain instead (runbook choice §10.5).
4. Connector env (enables LIVE check publishing; absent ⇒ dry-run logging):
   `CISYNC_CONN_GITHUB_APP_ID`, `CISYNC_CONN_PRIVATE_KEY_FILE`,
   `CISYNC_CONN_INSTALLATION_ID` (default install; multi-install resolves automatically),
   `CISYNC_CONN_DETAILS_URL=http://localhost:3000`.
5. Smoke: open a PR on an installed repo → within seconds the ledger gains
   `delivery.accepted → intent.declared(origin=github_webhook) → candidate.submitted`
   and the PR shows check **Agent Verification Gate** in `queued`.

## 3. Required-check configuration (branch protection)

Admins configure this themselves — CISync never requests Administration rights:

Settings → Branches → `<base>` → Require pull request before merging →
Require status checks → select **Agent Verification Gate** (exact string;
it is a compatibility contract — never rename without a migration note).
Leave "require branches up-to-date" OFF initially: CISync's merge-base
invalidation enforces freshness more surgically than strict rebasing.

## 4. Secret rotation (zero-downtime)

```bash
# 1. dual window
export CISYNC_INGEST_WEBHOOK_SECRETS="NEW_SECRET,OLD_SECRET"
# restart ingest; old-signed deliveries still verify
# 2. update the secret in GitHub App settings to NEW_SECRET
# 3. after ≤24h overlap
export CISYNC_INGEST_WEBHOOK_SECRETS="NEW_SECRET"
```
Alert if the overlap window exceeds 24h. Internal ctrl↔connector secrets rotate
independently with the same dual-accept trick.

## 5. Re-run policy

`CISYNC_CONN_RERUN_POLICY=replan|replay_cached` (default replan).
Caps: `CISYNC_CONN_RERUN_MAX_PER_CANDIDATE=2`,
`CISYNC_CONN_RERUN_RATE_PER_HOUR=20`. Over-cap flips the check neutral with
"budget exhausted" — a required check is never silently ignored.
Write budget: `CISYNC_CONN_WRITE_BUDGET_PER_HOUR=300` per installation;
exhaustion queues writes (never drops). Stalled checks (>45m non-terminal)
flip neutral via sweeper.

## 6. Suspension / uninstall

`installation.deleted` webhook ⇒ repos marked suspended: check writes stop
instantly (dry-run logs continue), active leases revoked, queued validations
cancel, synthetic-intent creation halts. Ledger rows are retained forever by
design (tamper-evidence IS the product); raw deliveries roll off at 30d.
Re-install creates a NEW installation id — no resurrection of old bindings.

## 7. Alerts that matter

| Signal | Meaning | First move |
|---|---|---|
| `chain verify` exit ≠ 0 | Ledger tampering or corruption | STOP; freeze writes; investigate per INVARIANTS I-07 |
| stalled installations row (`webhook_state=stalled`) | No deliveries >15m from a repo | check tunnel/proxy, then GitHub App hook settings |
| rate-limit exhaustion metrics on connector | Write budget too low or GitHub throttling | inspect pending_writes queue depth |
| p95 intent-create latency > 5s sustained | Pool/backlog pressure | check pg_stat_activity; see SPEC §3 starvation notes |
| human-decisions queue aging red | Agents blocked on approvals | staff the queue; this is the autonomy bottleneck |

## 8. Verification chain audit

Nightly (cron target for CI): `docker compose exec control-plane
/app/control-plane verify` — recomputes the hash chain and checkpoint
signatures; fails closed on mismatch.

## 9. Known limits (honest posture)

- Docker provider is NOT-FOR-PRODUCTION (THREAT_MODEL B5 graduation checklist).
- Single admin-token auth; RBAC = v0.3. Tenant predicates enforced everywhere.
- Replay seen-window (P1-1) deferred — GUID dedupe gives indefinite exact-replay protection.
- B7 dedicated audit stream table deferred; security events currently in structured logs.
