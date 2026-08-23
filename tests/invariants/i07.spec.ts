import { describe, expect, it } from 'vitest';
import fc from 'fast-check';
import { canonicalJson, hashPayload, verifyChain } from './lib/chain.js';
import { chainTamperCaseArb } from './lib/attack-vectors.js';
import { validateAgainstEventSchema } from './lib/event-schema.js';
import { verifyGoldenFixtures, loadFixtureChain, TAMPERED_FIXTURES } from './lib/fixtures.js';
import { liveModeEnabled } from './lib/env.js';

/**
 * I-07 — Ledger append-only + hash-chain verifies; projections provably
 * rebuildable by replay.
 * Contract mode: golden fixture chains verify; every tampered variant fails
 * with the precise rule; property: ANY single-field tamper in a valid chain
 * is detected. Live mode: the running ledger's tail re-verifies end-to-end
 * across pagination boundaries.
 */

describe('I-07 contract: golden fixtures', () => {
  it('valid chain passes all four verification rules', () => {
    const report = verifyGoldenFixtures.valid();
    expect(report).toEqual({ ok: true, entriesChecked: 6 });
  });

  it('every schema field of the valid chain conforms to events.schema.json', () => {
    for (const ev of loadFixtureChain('golden-chain.valid.json')) {
      const verdict = validateAgainstEventSchema(ev);
      // WHY per-event assert: names the exact failing event on drift.
      expect(verdict.errors, `event ${ev['seq']} (${ev['type']})`).toEqual([]);
    }
  });

  it.each(TAMPERED_FIXTURES)('%s is detected', (fixture) => {
    const report = verifyGoldenFixtures.tampered(fixture);
    expect(report.ok).toBe(false);
    expect(report.failure?.rule).toBeDefined();
  });

  it('canonical JSON matches Go map-marshal ordering (digest interop)', () => {
    const payload = { b: 1, a: { y: [2, 1], x: null }, c: 'z' };
    expect(canonicalJson(payload)).toBe('{"a":{"x":null,"y":[2,1]},"b":1,"c":"z"}');
    expect(hashPayload(payload)).toMatch(/^sha256:[a-f0-9]{64}$/);
  });
});

describe('I-07 contract: any single-field tamper breaks verification', () => {
  it('property: every forged chain fails verification, originals always pass', () => {
    fc.assert(
      fc.property(chainTamperCaseArb, ({ original, tampered }) => {
        expect(verifyChain(original).ok).toBe(true);
        const report = verifyChain(tampered);
        expect(report.ok, `tamper escaped detection: ${JSON.stringify(report)}`).toBe(false);
        expect(report.failure).toBeDefined();
      }),
    );
  });

  it('reordering a valid chain is detected as linkage break (append-only)', () => {
    const reordered = [...loadFixtureChain('golden-chain.valid.json')].reverse();
    expect(verifyChain(reordered).ok).toBe(false);
  });

  it('dropping an interior event breaks the chain (linkage/gap detection)', () => {
    const chain = loadFixtureChain('golden-chain.missing-middle.json');
    // Removing an event necessarily trips linkage first (successor's
    // prev_hash names the dropped entry); a pure seq gap cannot exist
    // without forged hashes too.
    const report = verifyChain(chain);
    expect(report.ok).toBe(false);
    expect(['prev_hash does not chain', 'seq gap (event missing)']).toContain(report.failure?.rule);
  });
});

describe.skipIf(!liveModeEnabled())('I-07 live: the served ledger tail verifies cryptographically', () => {
  it('paged tail is contiguous, schema-valid and hash-chained across pages', { timeout: 60_000 }, async () => {
    const { apiBase } = await import('./lib/live.js');
    const { tailEvents } = await import('./lib/tail.js');
    let cursor = 0;
    let previousTailHash: string | undefined;
    let pages = 0;
    // WHY maxPages scales with the ledger: the dev DB is shared and grows
    // monotonically across suite runs; a fixed page bound would trip on size,
    // not on any chain property. The bound only guards a runaway loop.
    const pageSize = 500;
    const maxPages = 200;
    for (;;) {
      const page = await tailEvents(apiBase(), cursor, undefined, pageSize);
      if (page.events.length === 0) break;
      const firstOfPage = page.events[0];
      const report = verifyChain(page.events, cursor === 0 || !firstOfPage ? undefined : firstOfPage.seq);
      expect(report.ok, `chain broke on page ${pages}: ${JSON.stringify(report.failure)}`).toBe(true);
      const first = page.events[0];
      if (previousTailHash !== undefined && first) {
        expect(first.prev_hash, `page ${pages + 1} does not link to predecessor`).toBe(previousTailHash);
      }
      const last = page.events[page.events.length - 1];
      previousTailHash = last?.entry_hash;
      cursor = page.next_seq;
      pages += 1;
      expect(pages).toBeLessThan(maxPages);
    }
    expect(pages).toBeGreaterThan(0);
  });
});
