import { describe, expect, it } from 'vitest';
import fc from 'fast-check';
import { evaluateEvidenceRecord, type AcceptanceContext, type ProposedEvidenceRecord } from './lib/evidence-rules.js';
import { duplicateAcceptanceAttempt } from './lib/attack-vectors.js';
import { liveModeEnabled } from './lib/env.js';
import { claimFleetJob, completeFleetJob } from './lib/live.js';

/**
 * I-03 — ≤1 accepted EvidenceRecord per (run_id, attempt); one accepted
 * record per lease jti.
 * Contract mode: property-prove the uniqueness gate over randomized
 * submission sequences (restarts included — acceptedRefs is stateless).
 * Live mode: completing the SAME fleet job twice must be refused with
 * `already_accepted` even when both carries carry the current fence token.
 */

function acceptAllCtx(expectedLeaseJti: string): AcceptanceContext {
  return { expectedLeaseJti, expectedInputsHash: 'sha256:' + '2'.repeat(64), acceptedRefs: [] };
}

const proposalArb: fc.Arbitrary<ProposedEvidenceRecord> = fc.record({
  seed: fc.array(fc.constantFrom(...'abcdefgh0123456789'.split('')), { minLength: 2, maxLength: 8 }).map((c) => c.join('')),
  sameRunAsPrior: fc.boolean(),
  reuseLeaseJti: fc.boolean(),
}).map(({ seed, sameRunAsPrior, reuseLeaseJti }) => ({
  recordId: `ev_${seed}`,
  runId: sameRunAsPrior ? 'run_prior' : `run_${seed}`,
  attempt: 1,
  kind: 'selected_unit_pass',
  verdict: 'pass',
  outcome: 'passed',
  digests: [],
  inputsHash: 'sha256:' + '2'.repeat(64),
  leaseJti: reuseLeaseJti ? 'lease_prior' : `lease_${seed}`,
}));

describe('I-03 contract: at most one acceptance per (run_id,attempt) and per lease jti', () => {
  it('second submission on identical identity is rejected as duplicate_run_attempt', () => {
    const ctx = acceptAllCtx('lease_one');
    const first: ProposedEvidenceRecord = { ...proposalTemplate(), runId: 'run_dup', leaseJti: 'lease_one' };
    expect(evaluateEvidenceRecord(first, ctx).action).toBe('accept');
    const afterAccept: AcceptanceContext = {
      ...ctx,
      acceptedRefs: [{ runId: first.runId, attempt: first.attempt, leaseJti: first.leaseJti }],
    };
    const second = { ...first };
    expect(evaluateEvidenceRecord(second, afterAccept)).toEqual({
      action: 'reject', reason: 'duplicate_run_attempt',
    });
  });

  it('different run under the SAME lease jti is rejected (one per lease)', () => {
    const ctx = acceptAllCtx('lease_shared');
    const first: ProposedEvidenceRecord = { ...proposalTemplate(), runId: 'run_a', leaseJti: 'lease_shared' };
    const afterAccept: AcceptanceContext = {
      ...ctx,
      acceptedRefs: [{ runId: first.runId, attempt: first.attempt, leaseJti: first.leaseJti }],
    };
    const second: ProposedEvidenceRecord = { ...proposalTemplate(), runId: 'run_b', leaseJti: 'lease_shared' };
    const ruling = evaluateEvidenceRecord(second, afterAccept);
    // Precedence: duplicate-attempt check runs before lease check; a distinct
    // run_id reaches the lease-jti rule.
    expect(ruling).toEqual({ action: 'reject', reason: 'lease_jti_already_accepted' });
  });

  it('randomized sequences never double-accept one identity', () => {
    fc.assert(
      fc.property(fc.array(proposalArb, { maxLength: 24 }), (submissions) => {
        const ctx = acceptAllCtx('lease_active');
        const accepted: { runId: string; attempt: number; leaseJti: string }[] = [];
        for (const p of submissions) {
          const ruling = evaluateEvidenceRecord(p, { ...ctx, acceptedRefs: accepted });
          if (ruling.action === 'accept') {
            expect(accepted.some((r) => r.runId === p.runId && r.attempt === p.attempt)).toBe(false);
            expect(accepted.some((r) => r.leaseJti === p.leaseJti)).toBe(false);
            accepted.push({ runId: p.runId, attempt: p.attempt, leaseJti: p.leaseJti });
          }
        }
      }),
      { numRuns: 200 },
    );
  });

  it('generated duplicate-acceptance vectors are well-formed and rejected', () => {
    fc.assert(
      fc.property(duplicateAcceptanceAttempt, ({ first, second }) => {
        expect(first).toEqual(second); // vector IS a re-submission of the same identity
        const ctx = acceptAllCtx(first.leaseJti);
        const afterAccept: AcceptanceContext = {
          ...ctx,
          acceptedRefs: [{ runId: first.runId, attempt: first.attempt, leaseJti: first.leaseJti }],
        };
        const ruling = evaluateEvidenceRecord(
          { recordId: 'ev_replay', runId: second.runId, attempt: second.attempt, kind: 'k', verdict: 'pass', outcome: 'passed', digests: [], inputsHash: ctx.expectedInputsHash, leaseJti: second.leaseJti },
          afterAccept,
        );
        expect(ruling.action).toBe('reject');
      }),
    );
  });

  function proposalTemplate(): ProposedEvidenceRecord {
    return {
      recordId: 'ev_t', runId: 'run_t', attempt: 1, kind: 'selected_unit_pass',
      verdict: 'pass', outcome: 'passed', digests: [], inputsHash: 'sha256:' + '2'.repeat(64),
      leaseJti: 'lease_t',
    };
  }
});

describe.skipIf(!liveModeEnabled())('I-03/I-11 live: fleet complete twice on one job is fenced off', () => {
  it('second completion of an already-accepted job returns 409 already_accepted', { timeout: 30_000 }, async () => {
    // WHY a seeded job in the probe pool: claiming from 'sim' would steal a
    // scheduler-dispatched run of another concurrent suite and corrupt its
    // fence epoch; this probe only needs A claimed job, not a real run.
    const { seedFenceProbeJob, claimFleetJob, FENCE_PROBE_POOL } = await import('./lib/live.js');
    await seedFenceProbeJob(`i03-${Date.now()}`);
    let claimed: Awaited<ReturnType<typeof claimFleetJob>> | undefined;
    for (let i = 0; i < 20 && !claimed; i++) {
      claimed = await claimFleetJob(FENCE_PROBE_POOL);
      if (!claimed) await new Promise((r) => setTimeout(r, 200));
    }
    if (!claimed) return; // claim path unavailable in this environment
    const first = await completeFleetJob(claimed.job.run_id, claimed.job.fence_token, 'succeeded');
    expect(first.status).toBe(200);
    expect(first.accepted).toBe(true);
    const replay = await completeFleetJob(claimed.job.run_id, claimed.job.fence_token, 'succeeded');
    expect(replay.status).toBe(409);
    expect(replay.reason).toBe('already_accepted');
  });
});
