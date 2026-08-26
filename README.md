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

Read `docs/ARCHITECTURE.md` before touching anything. Agents: read
`docs/REPO_STANDARDS.md` §6 first — it binds you.
