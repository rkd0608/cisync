# CISync — Agent-Native Verification & Integration Control Plane

Converts an unbounded stream of agent-generated code changes into prioritized,
deduplicated, evidence-backed validation decisions over a tamper-evident ledger.

## Quick start

```bash
make up          # postgres + all services via docker-compose
make test        # unit + property tests
make storm       # 500-candidate concurrency simulation against running stack
open http://localhost:3000
```

## Layout

| Path | What |
|---|---|
| `docs/` | ARCHITECTURE (normative), INVARIANTS, THREAT_MODEL, REPO_STANDARDS, ROADMAP |
| `packages/contracts/` | OpenAPI + event schemas — single source of truth |
| `services/ingest` | GitHub webhook edge (:8080) |
| `services/control-plane` | Domain, scheduler, evidence, leases, API (:8081) |
| `services/runner-fleet` | Execution providers sim/docker (:8082) |
| `services/github-connector` | Check write-back (W2) |
| `apps/web` | Change-graph dashboard (:3000) |
| `tests/` | invariants · e2e · scenarios |

## Execution providers & NOT-FOR-PRODUCTION banner

`CISYNC_FLEET_PROVIDER` selects the execution substrate; **`sim` is the default**
and executes nothing (deterministic simulation).

- **sim** (default): simulated durations/outcomes; safe by construction.
- **docker** — **NOT-FOR-PRODUCTION**: real containers, no repo code executed.
- **realexec** — **NOT-FOR-PRODUCTION until THREAT_MODEL graduation** (`docker build
  -f platform/tools/Dockerfile.tools -t cisync-tools:v0 .`, then
  `CISYNC_FLEET_PROVIDER=realexec`; enable control-plane materialization via
  `CISYNC_CTRL_REPO_BUNDLES_DIR=/repos` + a GitHub credential source). Runs REAL
  eslint/tsc/compileall/go-vet checks on egress-denied, read-only-rootfs,
  resource-capped sandboxes against bundles the control-plane stages into the shared
  `cisync-repos` volume — runners hold NO GitHub tokens. Both non-sim providers share
  the THREAT_MODEL B5 banner: they are dev/demo postures, not multi-tenant isolation.

Read `docs/ARCHITECTURE.md` before touching anything. Agents: read
`docs/REPO_STANDARDS.md` §6 first — it binds you.
