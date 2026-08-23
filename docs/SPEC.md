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
| 2026-08-23 | integrator | `prefixedUlid` extended with `dlv_`: delivery aggregates are platform-minted ULIDs; external GitHub GUID lives only in `payload.ext_delivery_id`. | ✅ events.schema.json |
| 2026-08-23 | builder-1e | Invariant suites delivered as `iNN.spec.ts`; INVARIANTS.md test-column renamed to match. | ✅ INVARIANTS.md |
| 2026-08-23 | integrator | Charter ratified; SPEC index created. | n/a |
