// WHY event-sourcing: the v1 contract (openapi.yaml) ships no intent-list
// endpoint — only per-id GETs. The dashboard therefore derives its live board
// from the ledger tail (GET /v1/events) exactly like every other consumer,
// keeping the UI consistent with the append-only ledger (invariant I-07)
// instead of inventing an undocumented list API.
//
// Projection rules: events are applied in `seq` order regardless of arrival
// order; duplicate envelope ids are deduped (at-least-once delivery); unknown
// types are ignored; known types with malformed payloads are counted in
// `malformedCount` and skipped rather than crashing the board.
import { candidateSubmittedPayloadSchema, decisionRenderedPayloadSchema, intentDeclaredPayloadSchema, type EventEnvelope } from './event-schemas';

export interface BoardIntent {
  id: string;
  goal: string;
  riskClass: string;
  deadline: string | null;
  state: string;
  createdAtSeq: number;
}

export interface BoardCandidate {
  id: string;
  intentId: string;
  headSha: string | null;
  state: string;
  clusterId: string | null;
  relationToRep: string | null;
}

export interface BoardState {
  lastSeq: number;
  intents: Record<string, BoardIntent>;
  candidates: Record<string, BoardCandidate>;
  decisionsRendered: number;
  malformedCount: number;
  seenEventIds: Record<string, true>;
  timeline: EventEnvelope[];
}

export const TIMELINE_CAP = 100;

export function emptyBoard(): BoardState {
  return {
    lastSeq: 0,
    intents: {},
    candidates: {},
    decisionsRendered: 0,
    malformedCount: 0,
    seenEventIds: {},
    timeline: [],
  };
}

function withTimeline(board: BoardState, event: EventEnvelope): void {
  board.timeline = [event, ...board.timeline].slice(0, TIMELINE_CAP);
}

function applyOne(board: BoardState, event: EventEnvelope): void {
  switch (event.type) {
    case 'intent.declared': {
      const payload = intentDeclaredPayloadSchema.safeParse(event.payload);
      if (!payload.success) {
        board.malformedCount += 1;
        return;
      }
      const id = event.aggregate?.id ?? '';
      if (!id) {
        board.malformedCount += 1;
        return;
      }
      board.intents[id] = {
        id,
        goal: payload.data.goal,
        riskClass: payload.data.risk_class,
        deadline: payload.data.deadline ?? null,
        // Declared intents start exploring until a decision says otherwise.
        state: 'exploring',
        createdAtSeq: event.seq,
      };
      return;
    }
    case 'candidate.submitted': {
      const payload = candidateSubmittedPayloadSchema.safeParse(event.payload);
      if (!payload.success) {
        board.malformedCount += 1;
        return;
      }
      board.candidates[payload.data.candidate_id] = {
        id: payload.data.candidate_id,
        intentId: payload.data.intent_id,
        headSha: payload.data.head_sha ?? null,
        state: 'submitted',
        clusterId: null,
        relationToRep: null,
      };
      // First admitted candidate moves the intent exploring → validating.
      const intent = board.intents[payload.data.intent_id];
      if (intent && intent.state === 'exploring') {
        intent.state = 'validating';
      }
      return;
    }
    case 'decision.rendered': {
      const payload = decisionRenderedPayloadSchema.safeParse(event.payload);
      if (!payload.success) {
        board.malformedCount += 1;
        return;
      }
      board.decisionsRendered += 1;
      const verbToState = decideStateMap(payload.data.verb);
      if (payload.data.subject.type === 'candidate') {
        const candidate = board.candidates[payload.data.subject.id];
        if (candidate) {
          candidate.state = verbToState.candidate;
          // A terminal candidate decision moves its intent too (DOMAIN_MODEL
          // §1.1: validating -> merge_ready on Decision=eligible).
          const intent = board.intents[candidate.intentId];
          if (intent) intent.state = verbToState.intent;
        }
      }
      if (payload.data.subject.type === 'intent') {
        const intent = board.intents[payload.data.subject.id];
        if (intent) intent.state = verbToState.intent;
      }
      return;
    }
    default:
      // Unknown/reserved event types are ignored by design.
      return;
  }
}

// WHY approximate: ledger decisions are per-subject facts; mapping them onto
// the intent state machine keeps the board honest without a dedicated
// intent-state read model (none exists in v1).
function decideStateMap(verb: 'eligible_for_merge_train' | 'rejected' | 'deferred'): {
  candidate: string;
  intent: string;
} {
  if (verb === 'eligible_for_merge_train') return { candidate: 'eligible', intent: 'merge_ready' };
  if (verb === 'rejected') return { candidate: 'rejected', intent: 'rejected' };
  return { candidate: 'validating', intent: 'validating' };
}

export function applyEvents(board: BoardState, events: EventEnvelope[]): BoardState {
  const next: BoardState = {
    ...board,
    intents: { ...board.intents },
    candidates: { ...board.candidates },
    seenEventIds: { ...board.seenEventIds },
    timeline: [...board.timeline],
  };

  const ordered = [...events].sort((a, b) => a.seq - b.seq);
  for (const event of ordered) {
    next.lastSeq = Math.max(next.lastSeq, event.seq);
    if (next.seenEventIds[event.id]) continue;
    next.seenEventIds[event.id] = true;
    applyOne(next, event);
    withTimeline(next, event);
  }
  return next;
}

export interface BoardSummary {
  totalIntents: number;
  activeIntents: number;
  mergeReadyIntents: number;
  rejectedIntents: number;
  totalCandidates: number;
  eligibleCandidates: number;
  inFlightCandidates: number;
  decisionsRendered: number;
  malformedEvents: number;
}

const ACTIVE_INTENT_STATES = new Set([
  'exploring',
  'validating',
  'blocked',
  'repairing',
]);
const IN_FLIGHT_CANDIDATE_STATES = new Set([
  'submitted',
  'planned',
  'validating',
  'repairing',
  'blocked_representative',
]);

export function deriveSummary(board: BoardState): BoardSummary {
  const intents = Object.values(board.intents);
  const candidates = Object.values(board.candidates);
  return {
    totalIntents: intents.length,
    activeIntents: intents.filter((i) => ACTIVE_INTENT_STATES.has(i.state)).length,
    mergeReadyIntents: intents.filter((i) => i.state === 'merge_ready').length,
    rejectedIntents: intents.filter((i) => i.state === 'rejected').length,
    totalCandidates: candidates.length,
    eligibleCandidates: candidates.filter((c) => c.state === 'eligible').length,
    inFlightCandidates: candidates.filter((c) =>
      IN_FLIGHT_CANDIDATE_STATES.has(c.state),
    ).length,
    decisionsRendered: board.decisionsRendered,
    malformedEvents: board.malformedCount,
  };
}
