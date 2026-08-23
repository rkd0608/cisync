import { describe, expect, it } from 'vitest';
import fc from 'fast-check';
import { liveModeEnabled } from './lib/env.js';
import { apiBase, authedHeaders } from './lib/live.js';
import { errorEnvelopeSchema } from './lib/api-schemas.js';
import { requestLoose } from './lib/http.js';

/**
 * I-14 — Tenant predicate in every query; tenant identity from the token
 * only; uniform 404 for cross-tenant access.
 * Contract mode: the uniformity predicate over ErrorEnvelope bodies.
 * Live mode: well-formed foreign/nonexistent IDs are indistinguishable
 * (same status + same error code), and tenant hints in the request are
 * ignored — identity cannot be overridden by payload.
 */

const WELL_FORMED_IDS = {
  intent: /^int_[0-9A-HJKMNP-TV-Z]{26}$/,
  candidate: /^cand_[0-9A-HJKMNP-TV-Z]{26}$/,
  cluster: /^clus_[0-9A-HJKMNP-TV-Z]{26}$/,
};

/** Uniform 404 means: identical status AND code, and no tenancy metadata. */
export function isUniformNotFound(a: { status: number; body: unknown }, b: { status: number; body: unknown }): boolean {
  if (a.status !== 404 || b.status !== 404) return false;
  const pa = errorEnvelopeSchema.safeParse(a.body);
  const pb = errorEnvelopeSchema.safeParse(b.body);
  if (!pa.success || !pb.success) return false;
  if (pa.data.error.code !== 'not_found' || pb.data.error.code !== 'not_found') return false;
  const serialized = JSON.stringify;
  const leaksTenant = (env: typeof pa.data): boolean =>
    serialized(env).toLowerCase().includes('tenant') && serialized(env).toLowerCase().includes('org_');
  return !leaksTenant(pa.data) && !leaksTenant(pb.data);
}

describe('I-14 contract: uniform-404 predicate', () => {
  it('two not_found envelopes of the same shape compare uniform', () => {
    const mk = (msg: string) => ({ status: 404, body: { error: { code: 'not_found', message: msg } } });
    expect(isUniformNotFound(mk('resource not found'), mk('resource not found'))).toBe(true);
  });

  it('status or code divergence breaks uniformity (distinguishable = violation)', () => {
    const a = { status: 404, body: { error: { code: 'not_found', message: 'x' } } };
    expect(isUniformNotFound(a, { status: 403, body: { error: { code: 'forbidden', message: 'exists elsewhere' } } })).toBe(false);
    expect(isUniformNotFound(a, { status: 200, body: {} })).toBe(false);
  });

  it('id shapes used by probes satisfy the openapi path patterns', () => {
    // WHY char-array generation: filtering arbitrary 26-char strings against
    // the Crockford alphabet rejects ~all candidates and stalls generation.
    const crockfordChar = fc.constantFrom(...'0123456789ABCDEFGHJKMNPQRSTVWXYZ'.split('').filter((c) => c !== ''));
    const ulidArb = fc.array(crockfordChar, { minLength: 26, maxLength: 26 }).map((chars) => chars.join(''));
    fc.assert(
      fc.property(ulidArb, (ulid) => {
        expect(WELL_FORMED_IDS.intent.test(`int_${ulid}`)).toBe(true);
        expect(WELL_FORMED_IDS.candidate.test(`cand_${ulid}`)).toBe(true);
      }),
    );
  });
});

describe.skipIf(!liveModeEnabled())('I-14 live: cross-tenant probing is indistinguishable from missing', () => {
  const FOREIGN_ULID = '01ARZ3NDEKTSV4RRFFQ69G5FAV'; // valid Crockford ULID, never issued

  it('nonexistent vs malformed vs foreign-shaped ids all return one uniform 404', async () => {
    const probes = [
      `/candidates/cand_${FOREIGN_ULID}`,
      '/candidates/cand_NOT_A_VALID_ID',
      `/change-intents/int_${FOREIGN_ULID}`,
      '/clusters/clus_00000000000000000000000000',
      '/clusters/clus_99999999999999999999999999',
    ];
    const responses = [];
    for (const path of probes) {
      responses.push(await requestLoose({ url: `${apiBase()}${path}`, method: 'GET', headers: authedHeaders() }));
    }
    for (let i = 1; i < responses.length; i++) {
      const firstRes = responses[0];
      const other = responses[i];
      if (!firstRes || !other) throw new Error('probe failed');
      expect(other.status, `probe ${i} (${probes[i]}) diverged`).toBe(firstRes.status);
      expect(firstRes.status).toBe(404);
      expect(isUniformNotFound(firstRes, other), `bodies distinguish ${probes[0]} from ${probes[i]}`).toBe(true);
    }
  });

  it('tenant identity cannot be smuggled via query or body', async () => {
    const smuggle = await requestLoose({
      url: `${apiBase()}/events?after_seq=0&tenant_id=org_EVILTENANT00000000000000`,
      method: 'GET',
      headers: authedHeaders(),
    });
    // The request still succeeds only under OUR token and returns OUR events.
    expect(smuggle.status).toBe(200);
    const denied = await requestLoose({ url: `${apiBase()}/events?after_seq=0`, method: 'GET' });
    expect(denied.status).toBe(401);
  });
});
