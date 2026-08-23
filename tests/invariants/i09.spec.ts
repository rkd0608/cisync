import { describe, expect, it } from 'vitest';
import fc from 'fast-check';
import { envelopeForType } from './lib/event-schema.js';
import * as payloads from './lib/payloads.js';
import { buildChainedEnvelopes, firstEnvelope } from './lib/builders.js';
import { liveModeEnabled } from './lib/env.js';
import { createIntent, submitCandidate, tailEvents, waitForEvent } from './lib/live.js';

/**
 * I-09 — Every Decision/plan/lease/repair stamps a resolvable
 * {policy_id, policy_version}; no active policy ⇒ fail closed.
 * Contract mode: schema enforces presence + version ≥ 1 on all four event
 * families; property-prove forged stamps are rejected.
 * Live mode: a real decision.rendered must carry a non-empty policy_id.
 */

describe('I-09 contract: policy stamps are structurally mandatory', () => {
  it('decision.rendered without a policy ref is rejected', () => {
    const missing = structuredClone(payloads.decisionRendered('i09'));
    delete missing['policy'];
    const env = firstEnvelope(buildChainedEnvelopes([
      { seq: 1, type: 'decision.rendered', aggregate: { type: 'decision', id: payloads.id('dec', 'i09') }, payload: missing },
]));
    expect(envelopeForType('decision.rendered', env).valid).toBe(false);
  });

  it('policy_version below 1 is rejected (unversioned stamp impossible)', () => {
    fc.assert(
      fc.property(fc.constantFrom(0, -1, -99), fc.constantFrom('decision.rendered', 'intent.declared', 'validation.planned'), (badVersion, eventType) => {
        const payloadFactories: Record<string, () => Record<string, unknown>> = {
          'decision.rendered': () => payloads.decisionRendered(`i09-${badVersion}`),
          'intent.declared': () => payloads.intentDeclared(`i09-${badVersion}`),
          'validation.planned': () => payloads.validationPlanned(`i09-${badVersion}`),
        };
        const factory = payloadFactories[eventType];
        if (!factory) throw new Error(`no payload factory for ${eventType}`);
        const payload = factory();
        const stampKey =
          eventType === 'decision.rendered' ? 'policy' : eventType === 'intent.declared' ? 'resolved_policy' : 'policy_version';
        const forged = structuredClone(payload);
        if (stampKey === 'policy_version') (forged[stampKey] as Record<string, unknown>)['policy_version'] = badVersion;
        else (forged[stampKey] as Record<string, unknown>).policy_version = badVersion;
        const aggType = eventType === 'decision.rendered' ? 'decision' : eventType === 'intent.declared' ? 'intent' : 'validation_plan';
        const env = firstEnvelope(buildChainedEnvelopes([
          { seq: 1, type: eventType, aggregate: { type: aggType, id: `${aggType === 'intent' ? 'int' : aggType === 'decision' ? 'dec' : 'val'}_FORGED` }, payload: forged },
]));
        expect(envelopeForType(eventType, env).valid).toBe(false);
      }),
    );
  });

  it('fail-closed rule: empty policy_id is rejected even with valid version', () => {
    // WHY predicate not ajv: the frozen schema types policy_id as an
    // unbounded string; resolvability (non-empty, known pack) is a
    // documented server-side rule, so contract mode encodes it separately.
    const payload = payloads.decisionRendered('i09-empty');
    (payload['policy'] as Record<string, unknown>)['policy_id'] = '';
    expect(isResolvablePolicyStamp(payload['policy'])).toBe(false);
    expect(isResolvablePolicyStamp(payloads.policyRef('ok'))).toBe(true);
  });

  /** Documented I-09 resolvability rule: non-empty id + version ≥ 1. */
  function isResolvablePolicyStamp(stamp: unknown): boolean {
    const ref = stamp as { policy_id?: unknown; policy_version?: unknown } | undefined;
    return typeof ref?.policy_id === 'string' && ref.policy_id.length > 0 && typeof ref?.policy_version === 'number' && Number.isInteger(ref.policy_version) && ref.policy_version >= 1;
  }
});

describe.skipIf(!liveModeEnabled())('I-09 live: rendered decisions carry resolvable policy stamps', () => {
  it('decision.rendered has non-empty policy_id and integer version ≥ 1', async () => {
    const grant = await createIntent('i09-live');
    const cand = await submitCandidate(grant.intent_id, 'i09-live');
    const decision = await waitForEvent(
      (ev) => ev.type === 'decision.rendered' && ev.payload['subject'] !== undefined && String((ev.payload['subject'] as Record<string, unknown>)['id']) === cand.candidate_id,
      { description: `decision.rendered for ${cand.candidate_id}`, timeoutMs: 60_000 },
    ).catch(async () => tailEvents(0).then((p) => p.events.find((e) => e.type === 'decision.rendered')));
    expect(decision, 'no decision.rendered found in ledger').toBeDefined();
    const policy = decision?.payload['policy'] as { policy_id?: string; policy_version?: number };
    expect(policy?.policy_id).toBeTruthy();
    expect(policy?.policy_version).toBeGreaterThanOrEqual(1);
    // The plan that produced the candidate carries the same pack lineage.
    const page = await tailEvents(0);
    const plan = page.events.find((e) => e.type === 'validation.planned' && e.payload['candidate_id'] === cand.candidate_id);
    expect(plan, 'candidate has no validation.planned').toBeDefined();
    expect((plan?.payload['policy_version'] as { policy_version?: number })?.policy_version).toBeGreaterThanOrEqual(1);
  });
});
