import { describe, expect, it } from 'vitest';
import { applyEvents, emptyBoard } from './event-board';
import { isFeedStale } from './event-board';
import {
  ALL_FILTERS,
  boardQueryString,
  candidateMatchesFilters,
  distinctOrigins,
  distinctRepos,
  groupIntents,
  intentMatchesFilters,
  parseBoardFilters,
} from './board-filters';
import type { BoardCandidate, BoardIntent } from './event-board';
import type { EventEnvelope } from './event-schemas';

// ---- fixture events → projected board -------------------------------------

const INT_A = 'int_01J8ZC7XK9Q2W3E4R5T6Y70001';
const INT_B = 'int_01J8ZC7XK9Q2W3E4R5T6Y70008';
const CAND_A = 'cand_01J8ZC7XK9Q2W3E4R5T6Y70002';

function envelope(seq: number, type: string, aggregateId: string, actor: 'agent' | 'human' | 'github', payload: Record<string, unknown>): EventEnvelope {
  return {
    id: `evt_01J8ZC7XK9Q2W3E4R5T6Y${String(seq).padStart(5, '0')}`,
    seq,
    type,
    version: 1,
    tenant_id: 'org_test',
    aggregate: { type: 'intent', id: aggregateId },
    causation_id: 'evt_root',
    correlation_id: 'corr_fixture',
    actor: { kind: actor },
    occurred_at: '2026-08-23T03:00:00Z',
    payload_sha256: 'aa'.repeat(32),
    prev_hash: 'bb'.repeat(32),
    entry_hash: 'cc'.repeat(32),
    payload,
  };
}

const fixtureEvents: EventEnvelope[] = [
  envelope(1, 'intent.declared', INT_A, 'agent', {
    goal: 'Fix duplicate payment retry',
    risk_class: 'high',
    repository: 'acme/payments',
  }),
  envelope(2, 'intent.declared', INT_B, 'human', {
    goal: 'Docs restructure',
    risk_class: 'low',
    repository: 'acme/docs-site',
  }),
  envelope(3, 'candidate.submitted', INT_A, 'github', {
    candidate_id: CAND_A,
    intent_id: INT_A,
    head_sha: 'a'.repeat(40),
  }),
];

const board = applyEvents(emptyBoard(), fixtureEvents);
const intents = Object.values(board.intents);
const candidates = Object.values(board.candidates);

describe('filter logic over fixture events', () => {
  it('projects origin and repository onto intents/candidates', () => {
    const payments = intents.find((i) => i.id === INT_A);
    expect(payments).toMatchObject({ repository: 'acme/payments', origin: 'agent', riskClass: 'high' });
    expect(candidates[0]).toMatchObject({ id: CAND_A, origin: 'github' });
  });

  it('matches all with default filters', () => {
    expect(intents.filter((i) => intentMatchesFilters(i, ALL_FILTERS))).toHaveLength(2);
    expect(candidates.filter((c) => candidateMatchesFilters(c, board.intents[c.intentId], ALL_FILTERS))).toHaveLength(1);
  });

  it('filters by exact risk and by origin', () => {
    expect(intents.filter((i) => intentMatchesFilters(i, { ...ALL_FILTERS, risk: 'high' })).map((i) => i.id)).toEqual([INT_A]);
    expect(intents.filter((i) => intentMatchesFilters(i, { ...ALL_FILTERS, origin: 'human' })).map((i) => i.id)).toEqual([INT_B]);
    expect(
      candidates.filter((c) =>
        candidateMatchesFilters(c, board.intents[c.intentId], { ...ALL_FILTERS, origin: 'github' }),
      ),
    ).toHaveLength(1);
  });

  it('filters repo as a substring match over the declared repository', () => {
    expect(
      intents.filter((i) => intentMatchesFilters(i, { ...ALL_FILTERS, repo: 'payments' })).map((i) => i.repository),
    ).toEqual(['acme/payments']);
    // Intents with no repository are dropped by a repo filter (never match-all).
    const noRepo = { ...intents[0], repository: null } satisfies BoardIntent;
    expect(intentMatchesFilters(noRepo, { ...ALL_FILTERS, repo: 'acme' })).toBe(false);
  });

  it('candidate filters inherit repo/risk from the parent intent', () => {
    const cand = candidates[0];
    expect(candidateMatchesFilters(cand, board.intents[INT_A], { ...ALL_FILTERS, repo: 'payments' })).toBe(true);
    expect(candidateMatchesFilters(cand, board.intents[INT_A], { ...ALL_FILTERS, risk: 'low' })).toBe(false);
    // Missing parent ⇒ repo/risk filters exclude the orphan row.
    expect(candidateMatchesFilters(cand, undefined, { ...ALL_FILTERS, risk: 'low' })).toBe(false);
  });
});

describe('query-param round trip', () => {
  it('serializes only non-default values and parses them back', () => {
    const qs = boardQueryString({ repo: 'payments', risk: 'high', origin: null }, 'risk');
    expect(qs).toBe('repo=payments&risk=high&group=risk');
    const parsed = parseBoardFilters(Object.fromEntries(new URLSearchParams(qs).entries()));
    expect(parsed).toEqual({
      filters: { repo: 'payments', risk: 'high', origin: null },
      groupBy: 'risk',
    });
  });

  it('defaults group to state and drops out-of-enum filter values', () => {
    const parsed = parseBoardFilters({
      group: 'risk',
      risk: 'catastrophic',
      origin: '<script>',
      repo: ['acme/payments'],
    });
    expect(parsed.groupBy).toBe('risk');
    expect(parsed.filters.risk).toBeNull();
    expect(parsed.filters.origin).toBeNull();
    expect(parsed.filters.repo).toBe('acme/payments');
  });

  it('treats malicious/empty query noise as "all"', () => {
    const parsed = parseBoardFilters({
      repo: '',
      risk: `${'x'.repeat(500)}`,
      group: '../../etc',
    });
    expect(parsed).toEqual({ filters: ALL_FILTERS, groupBy: 'state' });
  });
});

describe('group-by toggle over fixture events', () => {
  it('sections intents in canonical state order, omitting empty groups', () => {
    // The submitted candidate advanced INT_A exploring→validating in the
    // projection, so both sections exist and sort canonically.
    const sections = groupIntents(intents, 'state');
    expect(sections.map((s) => s.key)).toEqual(['exploring', 'validating']);
    expect(sections[1].items.map((i) => i.id)).toEqual([INT_A]);
  });

  it('sections intents in critical→low risk order', () => {
    const sections = groupIntents(intents, 'risk');
    expect(sections.map((s) => s.key)).toEqual(['high', 'low']);
  });

  it('respects the canonical order even when data arrives unordered', () => {
    const shuffled: BoardIntent[] = [
      { ...intents[0], id: 'a', riskClass: 'low', state: 'rejected' },
      { ...intents[0], id: 'b', riskClass: 'critical', state: 'merge_ready' },
      { ...intents[0], id: 'c', riskClass: 'medium', state: 'validating' },
      { ...intents[0], id: 'd', riskClass: 'high', state: 'exploring' },
    ];
    expect(groupIntents(shuffled, 'risk').map((s) => s.key)).toEqual([
      'critical', 'high', 'medium', 'low',
    ]);
    expect(groupIntents(shuffled, 'state').map((s) => s.key)).toEqual([
      'exploring', 'validating', 'merge_ready', 'rejected',
    ]);
  });
});

describe('distinct option lists for the filter bar', () => {
  it('collects sorted unique repos and known origins only', () => {
    expect(distinctRepos(intents)).toEqual(['acme/docs-site', 'acme/payments']);
    expect(distinctOrigins(intents, candidates)).toEqual(['agent', 'github', 'human']);
    // Unknown origin strings never become selectable options.
    const weird: BoardCandidate = { ...candidates[0], origin: 'ghost' };
    expect(distinctOrigins([], [weird])).toEqual([]);
  });
});

describe('feed staleness (>60s seq stall)', () => {
  const now = 1_800_000_000_000;

  it('flags stalls beyond 60s citing the last advance', () => {
    expect(isFeedStale(now - 61_000, now)).toBe(true);
    expect(isFeedStale(now - 60_000, now)).toBe(false);
    expect(isFeedStale(now - 5_000, now)).toBe(false);
  });

  it('never flags before the first advance (loading, not stale)', () => {
    expect(isFeedStale(null, now)).toBe(false);
  });
});
