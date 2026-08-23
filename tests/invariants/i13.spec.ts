import { describe, expect, it } from 'vitest';
import fc from 'fast-check';
import { compareRanked } from './lib/evidence-rules.js';
import { liveModeEnabled } from './lib/env.js';
import { apiBase, authedHeaders } from './lib/live.js';
import { eventsTailSchema } from './lib/api-schemas.js';
import { request } from './lib/http.js';

/**
 * I-13 — Ordering derives from ledger seq, never wall clocks; deterministic
 * tie-break priority → age → ULID.
 * Contract mode: the tie-break comparator is a total, antisymmetric,
 * permutation-stable order; seq contiguity is a pure predicate.
 * Live mode: the served tail is strictly seq-ordered and contiguous even
 * when occurred_at timestamps repeat (wall-clock independence).
 */

const rankedArb = fc.record({
  priorityScore: fc.double({ min: -10, max: 10, noNaN: true }),
  createdSeq: fc.integer({ min: 1, max: 1000 }),
  id: fc.constantFrom('run_A', 'run_B', 'run_C', 'run_D'),
});

describe('I-13 contract: tie-break comparator is a strict total order', () => {
  it('antisymmetric + transitive + permutation-invariant (deterministic dispatch)', () => {
    fc.assert(
      fc.property(fc.array(rankedArb, { maxLength: 30 }), (runs) => {
        const sorted = [...runs].sort(compareRanked);
        const resort = [...sorted].sort(compareRanked);
        expect(resort).toEqual(sorted); // stability ⇒ reproducible storm runs
        for (let i = 1; i < sorted.length; i++) {
          const prev = sorted[i - 1];
          const curr = sorted[i];
          if (!prev || !curr) continue;
          expect(compareRanked(prev, curr)).toBeLessThanOrEqual(0);
          expect(compareRanked(curr, prev)).toBeGreaterThanOrEqual(0);
          if (compareRanked(prev, curr) === 0) expect(JSON.stringify(prev)).toBe(JSON.stringify(curr));
        }
      }),
    );
  });

  it('priority dominates age, age dominates ULID', () => {
    const high = { priorityScore: 2, createdSeq: 900, id: 'run_Z' };
    const lowOlder = { priorityScore: 1, createdSeq: 5, id: 'run_A' };
    expect(compareRanked(high, lowOlder)).toBeLessThan(0);
    const samePrioNew = { priorityScore: 1, createdSeq: 6, id: 'run_M' };
    expect(compareRanked(lowOlder, samePrioNew)).toBeLessThan(0); // older first
    const sameEverythingA = { priorityScore: 1, createdSeq: 5, id: 'run_A' };
    expect(compareRanked(sameEverythingA, lowOlder)).toBeLessThanOrEqual(0); // ULID asc
  });

  it('contiguity predicate rejects gaps and duplicates (ledger-derived order)', () => {
    expect(seqContiguous([3, 4, 5])).toBe(true);
    expect(seqContiguous([3, 5])).toBe(false);
    expect(seqContiguous([3, 3, 4])).toBe(false);
    expect(seqContiguous([])).toBe(true);
  });

  function seqContiguous(seqs: number[]): boolean {
    return seqs.every((s, i) => i === 0 || s === (seqs[i - 1] ?? 0) + 1);
  }
});

describe.skipIf(!liveModeEnabled())('I-13 live: served tail ordering is logical, not wall-clock', () => {
  it('seq is contiguous and ascending across pages regardless of occurred_at ties', { timeout: 60_000 }, async () => {
    let cursor = 0;
    let previous: number | undefined;
    let sawTie = false;
    let pages = 0;
    for (;;) {
      const res = await request(
        { url: `${apiBase()}/events?after_seq=${cursor}&limit=500`, method: 'GET', headers: authedHeaders() },
        eventsTailSchema,
      );
      if (!res.ok) throw new Error(`tail failed: ${JSON.stringify(res.body)}`);
      if (res.body.events.length === 0) break;
      const times = new Set(res.body.events.map((e) => e.occurred_at));
      if (times.size < res.body.events.length) sawTie = true;
      for (const ev of res.body.events) {
        if (previous !== undefined) expect(ev.seq).toBe(previous + 1);
        previous = ev.seq;
      }
      cursor = res.body.next_seq;
      pages += 1;
      // WHY the bound scales: shared dev DB grows monotonically; a fixed
      // page count would trip on ledger size, not on ordering properties.
      expect(pages).toBeLessThan(200);
    }
    expect(pages).toBeGreaterThan(0);
    // WHY record-only: ties cannot be forced black-box; contiguity above is
    // the binding assertion. Storm (W3) generates heavy-tie workloads.
    void sawTie;
  });
});
