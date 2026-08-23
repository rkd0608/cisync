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
| 2026-08-23 | wave-2-integration | Compiled-in policy registry now serves TWO packs: the §8 payments document plus a wildcard fallback `pol_sauron_default` v1 — without it every low/medium-risk intent (and any non-payments repo) failed closed at planning (I-09). Most-specific-wins resolution unchanged; §8 payments pack byte-identical. | ⬜ proposed for ARCHITECTURE D7 note |
| 2026-08-23 | wave-2-integration | `delivery.accepted` aggregate ids are now platform-minted `dlv_`-prefixed ULIDs; external GitHub GUID lives only in `payload.ext_delivery_id` (emitters fixed in control-plane). | ✅ (schema already allowed `dlv_`) |
| 2026-08-23 | wave-2-integration | Added internal-protocols §4: control-plane→github-connector decision push (`POST /internal/connector/decisions`, HMAC via `SAURON_CONN_WEBHOOK_SECRET`) and the fleet completion feed (`GET /internal/fleet/jobs/completed`). | ✅ internal-protocols.md §4 |
| 2026-08-23 | builder-1e | Invariant suites delivered as `iNN.spec.ts`; INVARIANTS.md test-column renamed to match. | ✅ INVARIANTS.md |
| 2026-08-23 | integrator | Charter ratified; SPEC index created. | n/a |
