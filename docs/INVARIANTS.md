# Sauron Executable Invariants (FROZEN v1)

Every invariant: enforced in code at the named point AND covered by an automated test
(W1e owns suites under `tests/invariants/`; builders own unit-level property tests).
An invariant violation is a P0 bug — fix before any wave exits.

| ID | Invariant | Enforced at | Test suite |
|----|-----------|-------------|------------|
| I-01 | A skipped/quarantined test NEVER counts as positive evidence | evidence accept-time validator (`ctrl/evidence`) | `tests/invariants/i01.spec.ts` |
| I-02 | Evidence/artifact reuse only on FULL `inputs_hash` match (base SHA, lockfiles, flags, toolchain) | reuse key construction (`ctrl/planner`, `fleet/artifacts`) | `i02.spec.ts` |
| I-03 | ≤1 accepted EvidenceRecord per `(run_id, attempt)`; one accepted record per lease jti | DB unique idx + accept tx | `i03.spec.ts` |
| I-04 | Runner credentials scoped ≤ declared action/repo/tier/TTL | job-lease JWT claims + fleet verify | `i04.spec.ts` |
| I-05 | Repair patches confined to contract `allowed_paths` — validated SERVER-side pre-accept | repair apply gate (`ctrl/failure`) | `i05.spec.ts` |
| I-06 | Budget conservation: Σ reservations − Σ releases = current usage (crash-safe via ledger events) | lease/budget store txs | `i06.spec.ts` |
| I-07 | Ledger append-only + hash-chain verifies; projections provably rebuildable by replay | DB trigger + nightly `verify` job | `i07.spec.ts` |
| I-08 | Post-terminal events are logged-and-ignored on terminal aggregates (no state resurrection) | state machine guards | `i08.spec.ts` |
| I-09 | Every Decision/plan/lease/repair stamps resolvable `{policy_id, policy_version}`; no active policy ⇒ fail closed | policy resolver | `i09.spec.ts` |
| I-10 | Admission control precedes queueing: budgets/WIP caps atomically reserved; overflow ⇒ 429 `budget_exceeded`, never overrun | scheduler admit tx | `i10.spec.ts` |
| I-11 | Results accepted only from current fence-token holder; stale epochs rejected; expired leases killed+fenced by reconciler | fleet complete gate + reconciler | `i11.spec.ts` |
| I-12 | At-least-once delivery ⇒ exactly-once effects: every consumer dedupes via `processed_events` inside the effect tx | all consumers | `i12.spec.ts` |
| I-13 | Ordering derives from ledger `seq`, never wall clocks; deterministic tie-break priority→age→ULID | scheduler + projection builders | `i13.spec.ts` |
| I-14 | Tenant predicate present in every query; tenant_id from token only; uniform 404 cross-tenant | store layer review + e2e probe | `i14.spec.ts` |
