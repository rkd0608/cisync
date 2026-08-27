# CISync Env Plumbing Matrix — compose -> AWS prod source

Every `platform/docker-compose.yml` variable mapped to its production source in
this kit. Sources: **SM** = Secrets Manager `/cisync/<env>/<name>` (injected by
ECS at task start via execution role), **task-def** = hardcoded non-secret in
`modules/ecs/tasks_*.tf`, **tfvars** = operator-set Terraform variable
(non-secret values only), **RDS** = replaces the compose postgres container,
**unset** = deliberately left at the service's built-in default.

## Replaced infrastructure (not env vars)

| Compose piece | Prod replacement |
|---|---|
| postgres container (`POSTGRES_USER/PASSWORD/DB`, pgdata volume, init.sql) | RDS Postgres 16 `cisync` db, private subnets, AWS-managed master secret |
| `./dev-keys:/keys` ro mounts | per-task `keystore` init container writing PEMs onto emptyDir `/keys` |
| `webhook-forwarder` (cloudflared profile) | public ALB HTTPS listener (real URL, ACM cert) |

## ingest (:8080)

| Var | Prod source |
|---|---|
| CISYNC_INGEST_PG_DSN | SM `db_dsns` json-key `ingest_dsn` |
| CISYNC_INGEST_WEBHOOK_SECRET | SM `webhook_secret` |
| CISYNC_INGEST_ADDR | task-def `:8080` |
| CISYNC_INGEST_CTRL_URL | task-def `http://control-plane.cisync.local:8081` |

## control-plane (:8081)

| Var | Prod source |
|---|---|
| CISYNC_CTRL_PG_DSN | SM `db_dsns` json-key `ctrl_dsn` |
| CISYNC_CTRL_ADDR | task-def `:8081` |
| CISYNC_CTRL_FLEET_URL | task-def CloudMap URL |
| CISYNC_CTRL_CONNECTOR_URL | task-def CloudMap URL |
| CISYNC_CTRL_ADMIN_TOKEN | SM `admin_token` |
| CISYNC_CTRL_WEBHOOK_SECRET | SM `webhook_secret` |
| CISYNC_CTRL_LEDGER_KEY_FILE | task-def `/keys/ledger_ed25519.key` <- keystore init <- SM `ledger_key` |
| CISYNC_CTRL_JOBLEASE_KEY_FILE | task-def `/keys/joblease_ed25519.key` <- keystore init <- SM `joblease_key` |
| CISYNC_CTRL_RATE_LIMIT_PER_MIN | unset (600 was harness sizing; prod default stands) |
| CISYNC_CTRL_SCHED_BATCH | unset (same rationale) |
| CISYNC_CTRL_TICK_INTERVAL | unset (prod default tick) |
| CISYNC_CTRL_RECONCILE_INTERVAL | unset (prod default 30 s posture) |
| CISYNC_CTRL_STALE_RUN_MAX_AGE | unset (documented 2x15-min default) |

## runner-fleet (:8082)

| Var | Prod source |
|---|---|
| CISYNC_FLEET_PG_DSN | SM `db_dsns` json-key `fleet_dsn` |
| CISYNC_FLEET_ADDR | task-def `:8082` |
| CISYNC_FLEET_PROVIDER | task-def pinned `sim` — **FLAG (THREAT_MODEL B5)**: docker provider needs a host socket, impossible on Fargate and NOT-FOR-PRODUCTION anywhere until B5 graduation; this deployment validates orchestration only |
| CISYNC_FLEET_JOBLEASYPUB_KEY_FILE | task-def `/keys/joblease_ed25519.pub` <- keystore init <- SM `joblease_pub_key`. Code alt `CISYNC_FLEET_JOBLEASE_PUB_B64` exists but intentionally unused (uniform file-mount custody) |
| CISYNC_FLEET_SIM_WORKERS | unset (48 was harness sizing) |

## github-connector (:8083)

| Var | Prod source |
|---|---|
| CISYNC_CONN_PG_DSN | SM `db_dsns` json-key `conn_dsn` |
| CISYNC_CONN_ADDR | task-def `:8083` |
| CISYNC_CONN_WEBHOOK_SECRET | SM `conn_webhook_secret` |
| CISYNC_CONN_ADMIN_TOKEN | SM `conn_admin_token` |
| CISYNC_CONN_CTRL_URL | task-def CloudMap URL |
| CISYNC_CONN_CTRL_TOKEN | SM `admin_token` (shared ctrl bearer) |
| CISYNC_CONN_REPORT_COMMENTS | literal `false` (default; v0.3 opt-in — RUNBOOK §3.0) |
| CISYNC_CONN_DETAILS_URL | tfvars `connector_details_url` (defaults to https://<domain>) |
| CISYNC_CONN_GITHUB_APP_ID | tfvars `github_app_id` (non-secret numeric ID), live mode only |
| CISYNC_CONN_GITHUB_INSTALLATION_ID | tfvars `github_installation_id`, live mode only |
| CISYNC_CONN_GITHUB_PRIVATE_KEY_FILE | task-def `/keys/github-app.pem` <- keystore init <- SM `github_app_private_key_pem`, live mode only |

Dry-run posture preserved: without the trio (enable_connector_live_mode=false)
the connector logs would-be check payloads exactly like compose.

## web (:3000)

| Var | Prod source |
|---|---|
| CISYNC_API_URL | task-def CloudMap URL |
| CISYNC_CONNECTOR_URL | task-def CloudMap URL |
| CISYNC_ADMIN_TOKEN | SM `admin_token` (server-side only; never shipped to browser) |

**NEXT_PUBLIC_\* build args: NO GAP TODAY.** The UI talks through the
server-side `/api/cisync/*` proxy configured at RUNTIME (next.config.ts), so
nothing client-inlined exists or is needed. FLAG: any future NEXT_PUBLIC_*
variable becomes a build-time concern requiring a Dockerfile ARG + CI plumbing.

## Prod-only additions (no compose counterpart)

| Var | Source | Why |
|---|---|---|
| CISYNC_CTRL_TENANT_ID | task-def explicit ULID | D11 tenancy stamping, deterministic in prod |
| CISYNC_CTRL_VERIFY_INTERVAL | task-def `24h` | SPEC §3 H3 nightly chain verify |
| CISYNC_CTRL_AUDIT_RETENTION_DAYS | task-def `90` | THREAT_MODEL B7 audit floor |
| CISYNC_CTRL_TRACKED_BASE_BRANCHES | tfvars | merge-base advance / supersede cascade |
| PORT | task-def `3000` | Next.js listen port pin |

## Rotation note (flagged)

Ingest supports a rotation list `CISYNC_INGEST_WEBHOOK_SECRETS` (plural) for
zero-downtime GitHub secret rollover. Not wired into the base task def; during
rotation add it as a one-off task-def revision pointing at a second SM secret
for <=24h overlap (RUNBOOK §8 rollback posture), then revert.
