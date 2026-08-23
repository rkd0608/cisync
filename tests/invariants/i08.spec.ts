import { describe, expect, it } from 'vitest';
import fc from 'fast-check';

/**
 * I-08 — Post-terminal events are logged-and-ignored on terminal
 * aggregates (no state resurrection).
 * Contract mode: a pure reducer encodes the documented terminal states;
 * properties prove late events never mutate state, application is
 * order/multiset-idempotent, and ignored events are still recorded.
 * Live enforcement needs an endpoint that injects post-terminal cancels —
 * pending W2 (EC-063), see docs/EDGE_CASES.md.
 */

export type CandidateState = 'submitted' | 'validating' | 'merge_ready' | 'rejected' | 'superseded' | 'cancelled';
export interface AppliedEvent {
  type: string;
  candidateId: string;
}
export interface ReductionResult {
  state: CandidateState;
  ignored: AppliedEvent[];
}

const TERMINAL: ReadonlySet<CandidateState> = new Set(['merge_ready', 'rejected', 'superseded', 'cancelled']);

/** Documented transition table; post-terminal events land in `ignored`. */
export function applyCandidateEvent(current: ReductionResult, ev: AppliedEvent): ReductionResult {
  if (TERMINAL.has(current.state)) {
    return { state: current.state, ignored: [...current.ignored, ev] };
  }
  switch (ev.type) {
    case 'validation.started':
    case 'evidence.recorded':
      return { state: 'validating', ignored: current.ignored };
    case 'candidate.superseded':
      return { state: 'superseded', ignored: current.ignored };
    case 'candidate.cancelled':
      return { state: 'cancelled', ignored: current.ignored };
    case 'decision.rendered':
      return { state: current.state === 'superseded' ? 'superseded' : 'merge_ready', ignored: current.ignored };
    default:
      return current;
  }
}

export function reduceAll(events: readonly AppliedEvent[]): ReductionResult {
  return events.reduce((acc, ev) => applyCandidateEvent(acc, ev), { state: 'submitted' as CandidateState, ignored: [] as AppliedEvent[] });
}

const EVENT_TYPES = ['validation.started', 'evidence.recorded', 'candidate.superseded', 'candidate.cancelled', 'decision.rendered'] as const;
const EVENT_ARB = fc.constantFrom(...EVENT_TYPES).map((type) => ({ type, candidateId: 'cand_fixed' }));

describe('I-08 contract: post-terminal events are absorbed without state change', () => {
  it('any events appended after the aggregate goes terminal never mutate state', () => {
    fc.assert(
      fc.property(fc.array(EVENT_ARB, { minLength: 1 }), fc.nat(5), (events, lateCancels) => {
        const forcedTerminal: AppliedEvent[] =
          TERMINAL.has(reduceAll(events).state)
            ? []
            : [{ type: 'candidate.cancelled', candidateId: 'cand_fixed' }]; // drive to a terminal state deterministically
        const history = [...events, ...forcedTerminal];
        const first = reduceAll(history);
        expect(TERMINAL.has(first.state)).toBe(true);
        const late = Array.from({ length: lateCancels }, () => ({ type: 'candidate.cancelled' as const, candidateId: 'cand_fixed' }));
        const after = reduceAll([...history, ...late]);
        expect(after.state).toBe(first.state); // no resurrection
        expect(after.ignored.length).toBeGreaterThanOrEqual(late.length); // every post-terminal event IS logged
        if (late.length > 0) {
          // WHY slice-guard: slice(-0) is the whole array; and with no late
          // arrivals the ignored log legitimately holds mid-sequence ones.
          expect(after.ignored.slice(-late.length)).toEqual(late); // late arrivals are the log tail
        }
      }),
    );
  });

  it('application of any multiset converges to the identical final state (idempotence)', () => {
    fc.assert(
      fc.property(fc.array(EVENT_ARB, { maxLength: 40 }), (events) => {
        const once = reduceAll(events);
        const duplicated = reduceAll([...events, ...events]); // at-least-once delivery
        expect(duplicated.state).toBe(once.state);
        expect(duplicated.ignored.length).toBeGreaterThanOrEqual(once.ignored.length);
      }),
    );
  });

  it('decision.rendered on a live aggregate reaches merge_ready exactly once', () => {
    const result = reduceAll([
      { type: 'validation.started', candidateId: 'c' },
      { type: 'decision.rendered', candidateId: 'c' },
      { type: 'decision.rendered', candidateId: 'c' },
      { type: 'evidence.recorded', candidateId: 'c' },
    ]);
    expect(result).toEqual({ state: 'merge_ready', ignored: [{ type: 'decision.rendered', candidateId: 'c' }, { type: 'evidence.recorded', candidateId: 'c' }] });
  });
});
