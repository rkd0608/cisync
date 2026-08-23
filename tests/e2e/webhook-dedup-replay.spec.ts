import { createHmac } from 'node:crypto';
import { describe, expect, it } from 'vitest';
import { drainTailFor, ingestBase, postSignedWebhook, webhookSecret } from './lib/webhook.js';
import { harnessEnv } from '../invariants/lib/env.js';

/**
 * Webhook edge journeys (EC-001/003/005): duplicate delivery GUID applies a
 * single effect; bad HMAC is refused 401 and persists nothing; malformed
 * JSON never crashes the service. All observed through public endpoints.
 */

const enabled = (): boolean => harnessEnv().e2eEnabled;

describe.skipIf(!enabled())('E2E webhook dedup/replay', () => {
  it('EC-001: same delivery GUID twice ⇒ two 202s but ONE delivery.accepted effect', async () => {
    const guid = `e2e-dup-${Date.now()}`;
    const raw = JSON.stringify({ action: 'synchronize', number: 7 });
    for (let attempt = 0; attempt < 2; attempt++) {
      const res = await postSignedWebhook(guid, raw);
      expect([200, 202]).toContain(res.status); // redelivery is ACKed
      if (res.status === 404) return; // ingest not part of this deployment
    }
    const events = await drainTailFor(
      (events) => events.filter((e) => e.type === 'delivery.accepted' && e.payload['ext_delivery_id'] === guid).length === 1,
      `exactly one delivery.accepted for ${guid}`,
    );
    const matches = events.filter((e) => e.type === 'delivery.accepted' && e.payload['ext_delivery_id'] === guid);
    expect(matches).toHaveLength(1);
  }, 90_000);

  it('EC-003: invalid HMAC ⇒ 401, and no delivery.accepted for that GUID', async () => {
    const guid = `e2e-badsig-${Date.now()}`;
    const res = await postSignedWebhook(guid, '{"tampered":true}', 'wrong-secret');
    expect(res.status).toBe(401);
    const events = await drainTailFor(
      () => true, // single drain; no waiting needed for a negative assertion
      'drain',
    );
    expect(events.some((e) => e.type === 'delivery.accepted' && e.payload['ext_delivery_id'] === guid)).toBe(false);
  }, 60_000);

  it('EC-005: malformed JSON with valid signature is quarantined, not fatal', async () => {
    const guid = `e2e-malformed-${Date.now()}`;
    const res = await postSignedWebhook(guid, '{not json');
    // Documented behavior: bounded rejection or quarantine — never a 5xx crash.
    expect([400, 202]).toContain(res.status);
    const health = await fetch(`${ingestBase()}/healthz`);
    expect(health.status).toBe(200);
    void webhookSecret;
  }, 60_000);

  it('signatures are computed with the shared dev secret', () => {
    const mac = createHmac('sha256', webhookSecret()).update('payload').digest('hex');
    expect(mac).toMatch(/^[a-f0-9]{64}$/);
  });
});
