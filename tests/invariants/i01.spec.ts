import { describe, expect, it } from 'vitest';
import fc from 'fast-check';
import { envelopeForType } from './lib/event-schema.js';
import {
  evaluateEvidenceRecord,
  SKIP_OUTCOMES,
  type AcceptanceContext,
  type ProposedEvidenceRecord,
} from './lib/evidence-rules.js';
import { skippedPositiveEvidenceAttempt } from './lib/attack-vectors.js';
import * as payloads from './lib/payloads.js';
import { buildChainedEnvelopes, firstEnvelope, type EnvelopeLike } from './lib/builders.js';
import { liveModeEnabled } from './lib/env.js';
import { createIntent, drainEvents, getDossier, submitCandidate, waitForEvent } from './lib/live.js';

/**
 * I-01 — A skipped/quarantined test NEVER counts as positive evidence.
 * Contract mode: the frozen schema admits only verdict pass|fail, and the
 * documented accept-time precedence rejects skip outcomes outright.
 * Live mode: every dossier-accepted PASS evidence item must trace to a run
 * whose validation.completed status was `succeeded`.
 */

const CI_STATUSES = ['pass', 'fail', 'skipped', 'quarantined', 'filtered', 'error', 'timed_out', 'cancelled', '', 'PASS'];

function evidenceEnvelope(seed: string, verdict: string): EnvelopeLike {
  return firstEnvelope(buildChainedEnvelopes([
    {
      seq: 1,
      type: 'evidence.recorded',
      aggregate: { type: 'evidence', id: payloads.id('ev', `${seed}-${verdict}`) },
      payload: { ...payloads.evidenceRecorded(`${seed}-${verdict}`, 'pass'), ...(verdict === 'pass' ? {} : { verdict }) },
    },
]));
}

describe('I-01 contract: schema admits only executed pass|fail verdicts', () => {
  it('rejects evidence.recorded envelopes whose verdict is not pass|fail', () => {
    fc.assert(
      fc.property(fc.constantFrom(...CI_STATUSES), fc.array(fc.constantFrom(...'abc0123456789-'.split('')), { minLength: 1, maxLength: 8 }).map((c) => c.join('')), (verdict, seed) => {
        const result = envelopeForType('evidence.recorded', evidenceEnvelope(seed, verdict));
        expect(result.valid).toBe(verdict === 'pass' || verdict === 'fail');
      }),
    );
  });

  it('accepts the honest baseline envelope', () => {
    const result = validate(evidenceEnvelope('baseline-ok', 'pass'));
    expect(result.valid).toBe(true);
  });

  function validate(env: EnvelopeLike): ReturnType<typeof envelopeForType> {
    return envelopeForType('evidence.recorded', env);
  }
});

describe('I-01 contract: accept-time precedence rejects skipped-as-positive attempts', () => {
  const ctx: AcceptanceContext = {
    expectedLeaseJti: payloads.id('lease', 'ctx'),
    expectedInputsHash: 'sha256:' + '1'.repeat(64),
    acceptedRefs: [],
  };
  const baseRecord = (): ProposedEvidenceRecord => ({
    recordId: payloads.id('ev', 'base'),
    runId: payloads.id('run', 'base'),
    attempt: 1,
    kind: 'selected_unit_pass',
    verdict: 'pass',
    outcome: 'passed',
    digests: [`sha256:${'3'.repeat(64)}`],
    inputsHash: ctx.expectedInputsHash,
    leaseJti: ctx.expectedLeaseJti,
  });

  it('every generated skip-positive attempt is rejected with the I-01 reason', () => {
    fc.assert(
      fc.property(skippedPositiveEvidenceAttempt, ({ seed, outcome, verdict }) => {
        const ruling = evaluateEvidenceRecord(
          { ...baseRecord(), recordId: payloads.id('ev', seed), runId: payloads.id('run', seed), outcome, verdict },
          ctx,
        );
        expect(ruling).toEqual({ action: 'reject', reason: 'skip_quarantine_never_positive_evidence' });
      }),
    );
  });

  it('no skip outcome yields acceptance regardless of claimed verdict', () => {
    fc.assert(
      fc.property(fc.constantFrom(...SKIP_OUTCOMES), fc.constantFrom('pass', 'fail'), (outcome, verdict) => {
        const ruling = evaluateEvidenceRecord({ ...baseRecord(), outcome, verdict }, ctx);
        expect(ruling.action).not.toBe('accept');
      }),
    );
  });
});

describe.skipIf(!liveModeEnabled())('I-01 live: dossier pass-evidence traces only to succeeded runs', () => {
  it('no accepted pass evidence originates from a non-succeeded run', { timeout: 150_000 }, async () => {
    const grant = await createIntent('i01-live');
    const cand = await submitCandidate(grant.intent_id, 'i01-live');
    await waitForEvent(
      // WHY payload.subject.id: the frozen envelope aggregates
      // decision.rendered on the DECISION (dec_…), not the candidate;
      // matching on aggregate.id can never hit.
      (ev) =>
        ev.type === 'decision.rendered' &&
        String((ev.payload['subject'] as Record<string, unknown> | undefined)?.id) === cand.candidate_id,
      {
        description: `decision.rendered for ${cand.candidate_id}`,
        // Shared-stack reality: under full-suite concurrency the first
        // dispatch can trail the submission by a minute; the invariant
        // itself is about evidence provenance, not scheduler speed.
        timeoutMs: 90_000,
      },
    );
    // WHY drain (not first page): shared-stack suites advance global seq far
    // past one page; the candidate's own events must be found wherever they sit.
    const ledger = await drainEvents();
    const succeededRuns = new Set(
      ledger.filter((e) => e.type === 'validation.completed').filter((e) => e.payload['status'] === 'succeeded').map((e) => String(e.payload['run_id'])),
    );
    const runByEvidence = new Map(
      ledger.filter((e) => e.type === 'evidence.recorded').map((e) => [String(e.payload['ev_id']), String(e.payload['run_id'])]),
    );
    const dossier = await getDossier(cand.candidate_id);
    const positiveAccepted = dossier.evidence_accepted.filter((e) => e.verdict === 'pass');
    for (const accepted of positiveAccepted) {
      const runId = runByEvidence.get(accepted.ev_id);
      expect(runId, `pass evidence ${accepted.ev_id} has no ledger record`).toBeDefined();
      expect(succeededRuns.has(runId as string), `pass evidence ${accepted.ev_id} traces to non-succeeded run ${runId}`).toBe(true);
    }
  });
});
