# Sauron Delivery Roadmap

| Wave | Scope | Agents | Gate to exit |
|------|-------|--------|--------------|
| P0 Planning ✅ | 5 specialist drafts | architect, SRE, QA-strategist, domain, security | drafts reconciled into frozen docs + contracts |
| P0 Synthesis ✅ | ARCHITECTURE / INVARIANTS / THREAT_MODEL / ROADMAP / contracts / scaffold | chief (orchestrator) | contracts committed, monorepo scaffolded |
| W1 Build | 1a ledger+domain store · 1b scheduler+evidence+fencing · 1c ingest+fleet(+providers) · 1d web UI · 1e QA suites (invariants/property/e2e against contracts) | 5 parallel builders | `make hygiene` green, unit+property green, services boot in compose |
| W2 Integrate | github check-writer · cross-service wiring · compose-up integration suites green | integrator + connector builder | black-box integration suite green end-to-end |
| W3 Storm | storm simulator 500 concurrent candidates · chaos scenarios (kill runner, dup webhooks, base advance stampede) · fix seams | QA-storm + systems engineer | EDGE_CASES automatable rows green; storm asserts pass 3× |
| W4 Audit | independent systems-audit agent reviews invariants/concurrency/security diffs · docs polish · runbook · release tag v0.1.0 | auditor + docs | audit findings P0 fixed; DoD checklist signed |

Definition of Done (v1): TEST_STRATEGY_DRAFT §DoD checklist all green + REPO_STANDARDS
hygiene gate + storm green + audit clean.
