# Sauron Repository Standards (BINDING FOR ALL AGENTS)

> Every agent brief MUST reference this file AND `docs/ENGINEERING_CHARTER.md`
> (250-line file cap, TDD, zero loose types, dependency approvals). Violations block
> merge. The integrator wave runs `make hygiene` and reverts non-conforming files.

## 1. Principles

1. **One concern per file.** A file that needs "and" to describe it gets split.
2. **A place for everything.** If a target location isn't defined below, STOP
   and report — never invent a new top-level directory.
3. **No orphan artifacts.** No scratch files, no `.bak`, no `output.*`, no
   TODO-only stubs, no commented-out code blocks, no unused imports/exports.
4. **Names are contracts.** A reader must infer a file's contents from its name.

## 2. Canonical tree

```
sauron/
├── docs/
│   ├── ARCHITECTURE.md          # frozen architecture (synthesis of plans/)
│   ├── INVARIANTS.md            # I-01..I-nn executable invariants
│   ├── THREAT_MODEL.md          # security model
│   ├── REPO_STANDARDS.md        # this file
│   ├── ROADMAP.md               # phased delivery plan
│   ├── EDGE_CASES.md            # final matrix (from docs/plans drafts)
│   └── plans/                   # planning-wave drafts (historical record)
├── packages/
│   └── contracts/               # API/event source of truth
│       ├── openapi.yaml         # REST contract
│       └── events.schema.json   # event envelope + payloads ($defs)
├── services/                    # Go 1.23 modules, one dir per service
│   ├── ingest/
│   ├── control-plane/
│   ├── runner-fleet/
│   └── github-connector/
├── apps/
│   └── web/                     # Next.js app (App Router)
├── platform/                    # compose files, Dockerfiles refs, deploy
├── tests/
│   ├── invariants/              # property/invariant suites (TS)
│   ├── e2e/                     # black-box compose-up suites (TS)
│   └── scenarios/               # storm simulator inputs
├── .github/workflows/           # CI only
├── Makefile                     # single entrypoint: make <target>
└── README.md
```

## 3. Go conventions (services/*)

```
service/
├── cmd/<service>/main.go        # ONLY entrypoint; wiring only, no logic
├── internal/
│   ├── api/                     # HTTP handlers: <resource>_handler.go
│   ├── domain/                  # pure types+state machines, zero I/O deps
│   │   ├── <aggregate>.go       # one aggregate per file
│   │   └── <aggregate>_test.go
│   ├── store/                   # Postgres access, one file per aggregate
│   ├── scheduler/ | evidence/ | lease/ ...
│   └── config/config.go         # env parsing only
├── migrations/                  # NNNN_description.up.sql / .down.sql
│                                # NNNN = zero-padded seq, snake_case desc
├── go.mod                       # module sauron.dev/sauron/<service>
└── Dockerfile                   # multi-stage, distroless/debian-slim runtime
```

Rules:
- Files/dirs: `snake_case.go`; packages: short lowercase single words where
  possible; test files mirror source name with `_test.go`.
- Exported symbols need doc comments. No panics across package boundaries.
- Migrations are append-only once merged; never edit an applied migration.
- Config via env only, parsed in `internal/config`; no `os.Getenv` elsewhere.
- Errors: wrapped with `%w`, sentinel errors in `domain/errors.go`.

## 4. TypeScript / Next.js conventions (apps/web, tests/*)

- App Router under `apps/web/src/app`; components in `src/components`
  (`PascalCase.tsx`); hooks `use-<name>.ts`; lib helpers `src/lib/<name>.ts`.
- Strict TS, no `any` without a justification comment; types imported from
  generated contract types only (never hand-duplicate API shapes).
- Tests colocated as `<name>.test.ts(x)` or under tests/ per tree above.
- pnpm workspaces; no npm/yarn lockfiles committed.

## 5. Naming rules (all languages)

| Artifact            | Pattern                  | Example                    |
| ------------------- | ------------------------ | -------------------------- |
| Go files/packages   | lowercase_snake          | `validation_plan.go`       |
| Go exported symbols | PascalCase/camelCase     | `ValidationPlan`           |
| TS/React files      | kebab-case, comps Pascal | `change-graph.tsx`         |
| SQL migrations      | `NNNN_snake_desc.{up,down}.sql` | `0003_add_leases.up.sql` |
| Events              | `<aggregate>.<verb_past>` | `candidate.superseded`     |
| API paths           | `/v1/<kebab-resources>`  | `/v1/change-intents`       |
| IDs                 | `<prefix>_<ulid>`        | `int_01J...`, `cand_01J...`|
| Env vars            | `SAURON_<SERVICE>_<NAME>`| `SAURON_CTRL_PG_DSN`       |
| Branches            | `<wave>/<kebab-desc>`    | `w1/scheduler-core`        |

## 6. Agent conduct rules

1. Work ONLY inside directories assigned in your brief. Never touch shared or
   other agents' paths.
2. Before finishing: delete anything you created but abandoned.
3. Run the verification commands given in your brief (build/vet/test/format)
   and fix failures — do not hand back red code.
4. Do not run `git commit` unless your brief explicitly says so; the
   integrator commits per wave.
5. If you find a spec gap: implement the documented contract, note the gap in
   your final report. Never freelance new public interfaces.
6. Generated code goes in `*_generated.go` / `*.generated.ts` with generation
   instructions at top; never hand-edit generated files.

## 7. Hygiene gate (enforced)

- `make hygiene` = gofmt/golangci-lint + eslint/prettier/tsc + structure check
  (script verifying §2 tree, naming patterns, absence of forbidden files:
  `*.bak *.orig *~ .DS_Store tmp_* scratch_*`).
- CI fails on violations. Integrator may revert any non-conforming file.
