import { describe, expect, it } from 'vitest';
import type { EventEnvelope } from './event-schemas';
import {
  applyEvents,
  deriveSummary,
  emptyBoard,
  type BoardState,
} from './event-board';

const INTENT_ID = 'int_01J8ZC7XK9Q2W3E4R5T6Y70001';
const CAND_A = 'cand_01J8ZC7XK9Q2W3E4R5T6Y70002';
const CAND_B = 'cand_01J8ZC7XK9Q2W3E4R5T6Y70003';

let seqCounter = 0;
function envelope(type: string, payload: Record<string, unknown>, aggregateId: string): EventEnvelope {
  seqCounter += 1;
  return {
    id: `evt_01J8ZC7XK9Q2W3E4R5T6Y7${String(seqCounter).padStart(6, '0')}`,
    seq: seqCounter * 10,
    type,
    version: 1,
    tenant_id: 'org_test',
    aggregate: { type: 'intent', id: aggregateId },
    causation_id: 'evt_root',
    correlation_id: 'corr_1',
    actor: { kind: 'agent', id: 'agent:test' },
    occurred_at: '2026-08-23T03:00:00Z',
    payload_sha256: 'aa',
    prev_hash: 'bb',
    entry_hash: 'cc',
    payload,
  };
}

function intentDeclared(): EventEnvelope {
  return envelope(
    'intent.declared',
    {
      goal: 'Add idempotency keys to checkout',
      risk_class: 'high',
      owned_surfaces: ['services/checkout/**'],
      deadline: null,
    },
    INTENT_ID,
  );
}

function candidateSubmitted(id: string): EventEnvelope {
  return envelope(
    'candidate.submitted',
    { candidate_id: id, intent_id: INTENT_ID, head_sha: 'a'.repeat(40) },
    INTENT_ID,
  );
}

function decisionRendered(subjectType: string, subjectId: string, verb: string): EventEnvelope {
  return envelope(
    'decision.rendered',
    {
      decision_id: `dec_${subjectId}`,
      subject: { type: subjectType, id: subjectId },
      verb,
      confidence: 0.94,
      policy: { policy_id: 'pol_default', version: 1 },
    },
    subjectId,
  );
}

describe('applyEvents projection', () => {
  it('derives intents and candidates from an ordered stream', () => {
    const board = applyEvents(emptyBoard(), [
      intentDeclared(),
      candidateSubmitted(CAND_A),
    ]);
    expect(board.intents[INTENT_ID]?.state).toBe('validating');
    expect(board.intents[INTENT_ID]?.goal).toContain('idempotency');
    expect(board.candidates[CAND_A]?.state).toBe('submitted');
    expect(board.lastSeq).toBe(20);
  });

  it('is order-insensitive: same envelopes arriving shuffled yield the same board', () => {
    const declared = intentDeclared();
    const submitted = candidateSubmitted(CAND_A);
    const decided = decisionRendered('candidate', CAND_A, 'eligible_for_merge_train');
    const forward = applyEvents(emptyBoard(), [declared, submitted, decided]);
    const shuffled = applyEvents(emptyBoard(), [decided, submitted, declared]);
    expect(shuffled.candidates[CAND_A]?.state).toBe(
      forward.candidates[CAND_A]?.state,
    );
    expect(shuffled.intents[INTENT_ID]?.state).toBe(forward.intents[INTENT_ID]?.state);
    expect(forward.intents[INTENT_ID]?.state).toBe('merge_ready');
    expect(forward.candidates[CAND_A]?.state).toBe('eligible');
    expect(shuffled.lastSeq).toBe(forward.lastSeq);
  });

  it('dedupes duplicate delivery ids (at-least-once transport)', () => {
    const declared = intentDeclared();
    const once = applyEvents(emptyBoard(), [declared]);
    const twice = applyEvents(once, [declared]);
    expect(Object.keys(twice.intents)).toHaveLength(1);
    expect(twice.timeline).toHaveLength(1);
    // dedupe survives a fresh empty-board replay too
    const replayed = applyEvents(emptyBoard(), [declared, declared]);
    expect(Object.keys(replayed.intents)).toHaveLength(1);
  });

  it('ignores unknown event types without corrupting the board', () => {
    const board = applyEvents(emptyBoard(), [
      intentDeclared(),
      envelope('policy.override_requested', { chaos: true }, INTENT_ID),
      envelope('representative.promoted', {}, CAND_A),
    ]);
    expect(board.malformedCount).toBe(0);
    expect(board.intents[INTENT_ID]).toBeDefined();
    expect(board.candidates[CAND_A]).toBeUndefined();
  });

  it('counts known types with malformed payloads instead of crashing', () => {
    const bad = envelope('intent.declared', { goal: 42 }, INTENT_ID);
    const board = applyEvents(emptyBoard(), [bad]);
    expect(board.malformedCount).toBe(1);
    expect(board.intents[INTENT_ID]).toBeUndefined();
  });

  it('maps decision verbs onto candidate and intent states', () => {
    const base = applyEvents(emptyBoard(), [
      intentDeclared(),
      candidateSubmitted(CAND_A),
      candidateSubmitted(CAND_B),
    ]);
    const eligible = applyEvents(base, [
      decisionRendered('candidate', CAND_A, 'eligible_for_merge_train'),
    ]);
    expect(eligible.candidates[CAND_A]?.state).toBe('eligible');
    const rejected = applyEvents(eligible, [
      decisionRendered('intent', INTENT_ID, 'rejected'),
    ]);
    expect(rejected.intents[INTENT_ID]?.state).toBe('rejected');
    const deferredBoard = applyEvents(emptyBoard(), [
      intentDeclared(),
      candidateSubmitted(CAND_A),
      decisionRendered('candidate', CAND_A, 'deferred'),
    ]);
    expect(deferredBoard.candidates[CAND_A]?.state).toBe('validating');
  });

  it('keeps lastSeq advancing for filtered/ignored events (cursor safety)', () => {
    const event = envelope('lease.granted', { anything: true }, INTENT_ID);
    const board = applyEvents(emptyBoard(), [event]);
    expect(board.lastSeq).toBe(event.seq);
    expect(board.timeline).toHaveLength(1);
  });
});

describe('deriveSummary', () => {
  function boardWith(states: { intent?: string; candidates: string[] }): BoardState {
    let board = applyEvents(emptyBoard(), [intentDeclared()]);
    for (const cand of states.candidates) {
      board = applyEvents(board, [candidateSubmitted(cand)]);
    }
    if (states.intent) {
      board = applyEvents(board, [decisionRendered('intent', INTENT_ID, states.intent === 'merge_ready' ? 'eligible_for_merge_train' : 'rejected')]);
    }
    return board;
  }

  it('summarises queue/capacity honestly from derived state', () => {
    const summary = deriveSummary(
      boardWith({ candidates: [CAND_A, CAND_B], intent: 'merge_ready' }),
    );
    expect(summary.totalIntents).toBe(1);
    expect(summary.mergeReadyIntents).toBe(1);
    expect(summary.activeIntents).toBe(0);
    expect(summary.totalCandidates).toBe(2);
    expect(summary.inFlightCandidates).toBe(2);
    expect(summary.decisionsRendered).toBe(1);
    expect(summary.malformedEvents).toBe(0);
  });
});
