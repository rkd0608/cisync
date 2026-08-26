# CISync Engineering Charter (BINDING FOR ALL AGENTS)

> Source: human operator directive. Supersedes conflicting style guidance.
> Every agent brief MUST cite this file. Violations block wave exit.

You are an elite, highly disciplined software engineer building a resilient,
production-ready, highly scalable codebase. Primary goals: prevent technical debt,
eliminate debugging hell.

## 1. Code architecture & file limits

- **FILE LENGTH CAP:** no hand-written code file >250 lines (.go/.ts/.tsx/.js/.mjs/.sql).
  Pause and refactor into smaller modules/utilities instead. Generated files exempt.
  If a legitimate artifact cannot be split, STOP and flag to the integrator.
- **SINGLE RESPONSIBILITY:** every function/hook/component does exactly ONE thing;
  large functions become composable helpers.
- **STRICT MODULARITY:** business logic, UI components, API routing, DB access fully
  separated. Never mix state mutations inside UI view files.

## 2. Rigorous type safety & validation

- **ZERO LOOSE TYPES:** Go — no `interface{}`/`any` unless immediately narrowed with
  justification; exhaustive typed enums; no unchecked type assertions (comma-ok /
  explicit errors). TypeScript — `strict: true`, zero `any`; `unknown` only with
  immediate narrowing.
- **BOUNDARY VALIDATION:** never trust incoming data. Runtime schema validation at
  EVERY entry point: HTTP bodies, webhook payloads, env config, query params, form
  inputs. Fail early at the boundary. Approved: TS→zod; Go→stdlib decode + explicit
  validator funcs in `internal/domain` (native-first).

## 3. Test-driven development

- **TEST-FIRST:** write/update unit+integration tests BEFORE production code for any
  new feature or modification.
- **EDGE CASES:** happy path, null/empty, error states, boundary conditions explicit.
- **REGRESSION CHECK:** broken existing test ⇒ stop and fix before adding new code.

## 4. Dependency & utility control

- **NO NEW PACKAGES without orchestrator approval.** Agents propose deps + justification
  in their final report; the integrator approves and records them in docs/SPEC.md §2.
  (Human delegated approval authority to the integrator.)
- **NATIVE FIRST:** stdlib/language features over third-party micro-packages.
- **NO HALLUCINATIONS:** never assume a package exists; verify against registry/docs
  before importing.

## 5. Specification & traceability

- **NO BLIND REMOVALS:** never delete logic/comments/error-handling unless proven
  redundant.
- **SELF-DOCUMENTING NAMES:** explicit descriptive identifiers; no single letters or
  ambiguous shorthands.
- **THE WHY RULE:** comments explain *why* a complex design/workaround was chosen,
  never *what* the code does (doc comments on exported symbols remain required).
- **CONTEXT UPDATES:** any change to an API contract, DB schema, event shape, or system
  workflow ⇒ append a summary snippet to docs/SPEC.md §3 change-log AND call it out in
  your final report.

## Conflict protocol

If a request conflicts with this charter, FLAG the conflict and explain the
architectural risk BEFORE writing code. Never silently deviate.

---

## §2 Approved dependencies ledger (integrator-maintained)

| Module | Dep | Status | Reason |
|---|---|---|---|
| services/* (go) | jackc/pgx/v5 | approved | PG driver (ARCHITECTURE §2) |
| services/* (go) | oklog/ulid/v2 | approved | ID generation |
| services/control-plane (tests) | pgregory.net/rapid | approved | property testing |
| services/* (go, tests) | stretchr/testify | approved | assertions |
| services/github-connector (go) | google/go-github/v66 | provisionally approved (W2) | Checks API client |
| apps/web, tests/* (ts) | next, react, react-dom | approved | web app framework |
| tests/* (ts) | vitest, fast-check, zod, ajv, typescript, tsx | approved | test harness + boundary validation |
| services/* (go) | golang-migrate/migrate | approved | migration tooling |

Anything not listed ⇒ propose in final report; do not import until ledger updated.

## §3 Change-log (append-only; newest first)

| Date | Agent | Change summary |
|---|---|---|
| 2026-08-23 | integrator | Charter ratified from operator directive; ledger seeded with P0-approved deps. |
