import { describe, expect, it } from 'vitest';
import { createIntent, parseError, releaseLease, renewLease } from './lib/journey.js';
import { leaseRenewalSchema } from '../invariants/lib/api-schemas.js';
import { harnessEnv } from '../invariants/lib/env.js';

/**
 * Lease TTL lifecycle (EC-042 tail + openapi renew contract): an active
 * lease renews with an incremented count; a terminal lease refuses renewal
 * with conflict_state (expired_lease | revoked_lease) — the same gate the
 * TTL reconciler hits after expiry.
 */

const enabled = (): boolean => harnessEnv().e2eEnabled;

describe.skipIf(!enabled())('E2E lease TTL expiry semantics', () => {
  it('active lease renews; terminal lease refuses renewal with 409 conflict_state', async () => {
    const grant = await createIntent('lease-ttl');
    expect(grant.lease_id).toMatch(/^lease_/);

    const renewed = await renewLease(grant.lease_id, 1800);
    if (renewed.status === 200) {
      const parsed = leaseRenewalSchema.parse(renewed.body);
      expect(parsed.renewal_count).toBeGreaterThanOrEqual(1);
      expect(parsed.ttl_expires_at).toBeTruthy();
    } else {
      // Queue-positioned leases may refuse direct renewal; must still be typed.
      const err = parseError(renewed.body);
      expect(err.error.code).toBe('conflict_state');
    }

    // Release is idempotent per openapi; then any renewal MUST be fenced off.
    const releaseStatus = await releaseLease(grant.lease_id);
    expect([200, 204]).toContain(releaseStatus);

    const afterRelease = await renewLease(grant.lease_id, 1800);
    expect(afterRelease.status).toBe(409);
    const conflict = parseError(afterRelease.body);
    expect(conflict.error.code).toBe('conflict_state');
    const reasons = JSON.stringify(conflict.error.details ?? {});
    expect(reasons).toMatch(/expired_lease|revoked_lease/);
  }, 90_000);
});
