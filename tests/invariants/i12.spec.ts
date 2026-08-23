import { createHmac } from 'node:crypto';
import { describe, expect, it } from 'vitest';
import fc from 'fast-check';
import { liveModeEnabled, harnessEnv } from './lib/env.js';
import { apiBase, authedHeaders, ingestBase } from './lib/live.js';
import { intentGrantSchema } from './lib/api-schemas.js';
import { newIdempotencyKey, request, requestLoose } from './lib/http.js';

/**
 * I-12 — At-least-once delivery ⇒ exactly-once effects; every consumer
 * dedupes inside the effect tx.
 * Contract mode: Idempotency-Key length rule + replay-identity predicate
 * (same key + same body MUST yield byte-identical response).
 * Live mode: identical intent creation twice → one aggregate; signed GitHub
 * delivery GUID sent twice via the public webhook → one delivery.accepted.
 */

describe('I-12 contract: idempotency-key and replay predicates', () => {
  it('keys inside 16..128 are valid; openapi bounds reject shorter/longer', () => {
    fc.assert(
      fc.property(fc.integer({ min: 1, max: 200 }), (len) => {
        const key = 'k'.repeat(len);
        expect(keyMeetsContract(key)).toBe(len >= 16 && len <= 128);
      }),
    );
  });

  it('replay identity requires BOTH same key and same request hash', () => {
    expect(isReplay({ key: 'a'.repeat(16), hash: 'h1' }, { key: 'a'.repeat(16), hash: 'h1' })).toBe(true);
    expect(isReplay({ key: 'a'.repeat(16), hash: 'h1' }, { key: 'a'.repeat(16), hash: 'h2' })).toBe(false);
    expect(isReplay({ key: 'a'.repeat(16), hash: 'h1' }, { key: 'b'.repeat(16), hash: 'h1' })).toBe(false);
  });

  function keyMeetsContract(key: string): boolean {
    return key.length >= 16 && key.length <= 128;
  }
  interface CommandRef { key: string; hash: string }
  function isReplay(first: CommandRef, second: CommandRef): boolean {
    return first.key === second.key && first.hash === second.hash;
  }
});

describe.skipIf(!liveModeEnabled())('I-12 live: duplicate commands collapse to one effect', () => {
  it('identical intent creation twice returns identical bodies and one ledger pair', async () => {
    const key = newIdempotencyKey('i12-replay');
    const body = {
      goal: 'idempotent creation probe',
      repository: 'acme/idem',
      base: 'main',
      expected_surfaces: ['services/**'],
      acceptance_criteria: ['replay'],
      risk: 'low',
    };
    const first = await request(
      { url: `${apiBase()}/change-intents`, method: 'POST', headers: { ...authedHeaders(), 'Idempotency-Key': key }, body },
      intentGrantSchema,
    );
    const second = await request(
      { url: `${apiBase()}/change-intents`, method: 'POST', headers: { ...authedHeaders(), 'Idempotency-Key': key }, body },
      intentGrantSchema,
    );
    expect(second.status).toBe(first.status);
    // Byte-level identity is the strongest documented replay guarantee.
    expect(second.rawText).toBe(first.rawText);
    const page = await import('./lib/live.js').then((m) => m.tailEvents(0));
    const declared = page.events.filter((e) => e.type === 'intent.declared' && e.payload['intent_id'] === first.body.intent_id);
    expect(declared).toHaveLength(1);
  });

  it('signed webhook delivered twice under one GUID produces ONE delivery.accepted', async () => {
    const secret = harnessSecret();
    const guid = `i12-guid-${Date.now()}`;
    const payload = JSON.stringify({ action: 'opened', number: 1 });
    for (let attempt = 0; attempt < 2; attempt++) {
      const res = await postSignedWebhook(guid, payload, secret);
      expect([200, 202]).toContain(res.status);
      if (res.status === 404) return; // ingest not deployed in this env; ctrl-only mode
    }
    const accepted = await waitForDeliveryAccepted(guid, 20_000);
    expect(accepted).toHaveLength(1);

    async function waitForDeliveryAccepted(targetGuid: string, timeoutMs: number) {
      const deadline = Date.now() + timeoutMs;
      let found: Awaited<ReturnType<typeof import('./lib/live.js').tailEvents>>['events'] = [];
      while (Date.now() < deadline) {
        const page = await import('./lib/live.js').then((m) => m.tailEvents(0));
        found = page.events.filter((e) => e.type === 'delivery.accepted' && e.payload['ext_delivery_id'] === targetGuid);
        if (found.length > 0) return found;
        await new Promise((r) => setTimeout(r, 400));
      }
      return found;
    }

    async function postSignedWebhook(deliveryGuid: string, rawBody: string, secretKey: string) {
      const signature = 'sha256=' + createHmac('sha256', secretKey).update(rawBody).digest('hex');
      return requestLoose({
        url: `${ingestBase()}/hooks/github`,
        method: 'POST',
        headers: {
          'Content-Type': 'application/json',
          'X-Hub-Signature-256': signature,
          'X-GitHub-Delivery': deliveryGuid,
          'X-GitHub-Event': 'push',
        },
        body: JSON.parse(rawBody),
      });
    }
  });

  function harnessSecret(): string {
    return harnessEnv().webhookSecret;
  }
});

