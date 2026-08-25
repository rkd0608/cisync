// Client-side dashboard filtering/grouping (plan §2.3 minimal evolution):
// repo / risk / origin filters over projected board data, persisted to query
// params, plus the state|risk group-by. Pure functions — fully unit-testable
// against fixture events.
import { INTENT_STATES, RISK_CLASSES } from './api-schemas';
import type { BoardCandidate, BoardIntent } from './event-board';

export type BoardGroupMode = 'state' | 'risk';
export const GROUP_MODES: BoardGroupMode[] = ['state', 'risk'];

export const ORIGIN_KINDS = ['agent', 'human', 'service', 'github'] as const;

export interface BoardFilters {
  repo: string | null;
  risk: string | null;
  origin: string | null;
}

export const ALL_FILTERS: BoardFilters = { repo: null, risk: null, origin: null };

const RISK_SET = new Set<string>(RISK_CLASSES);
const ORIGIN_SET = new Set<string>(ORIGIN_KINDS);

function firstString(value: string | string[] | undefined): string | undefined {
  if (Array.isArray(value)) return typeof value[0] === 'string' ? value[0] : undefined;
  return value;
}

export interface ParsedBoardQuery {
  filters: BoardFilters;
  groupBy: BoardGroupMode;
}

// Query params are an input boundary (charter §2): unknown keys dropped,
// out-of-enum values fall back to "all"/default instead of throwing or
// rendering attacker-chosen state.
export function parseBoardFilters(query: Record<string, string | string[] | undefined>): ParsedBoardQuery {
  const repo = firstString(query.repo)?.trim() ?? '';
  const risk = firstString(query.risk) ?? '';
  const origin = firstString(query.origin) ?? '';
  const groupBy: BoardGroupMode = firstString(query.group) === 'risk' ? 'risk' : 'state';
  return {
    filters: {
      repo: repo.length > 0 ? repo : null,
      risk: RISK_SET.has(risk) ? risk : null,
      origin: ORIGIN_SET.has(origin) ? origin : null,
    },
    groupBy,
  };
}

// Defaults serialize to nothing so URLs stay canonical and shareable.
export function boardQueryString(filters: BoardFilters, groupBy: BoardGroupMode): string {
  const params = new URLSearchParams();
  if (filters.repo) params.set('repo', filters.repo);
  if (filters.risk) params.set('risk', filters.risk);
  if (filters.origin) params.set('origin', filters.origin);
  if (groupBy === 'risk') params.set('group', 'risk');
  return params.toString();
}

export function intentMatchesFilters(intent: BoardIntent, f: BoardFilters): boolean {
  if (f.repo !== null && !(intent.repository ?? '').includes(f.repo)) return false;
  if (f.risk !== null && intent.riskClass !== f.risk) return false;
  if (f.origin !== null && intent.origin !== f.origin) return false;
  return true;
}

// Repo/risk ride on the parent intent; origin may come from either the
// candidate's own submitter or its declaring actor.
export function candidateMatchesFilters(
  candidate: BoardCandidate,
  parent: BoardIntent | undefined,
  f: BoardFilters,
): boolean {
  if (f.repo !== null && !((parent?.repository ?? '')).includes(f.repo)) return false;
  if (f.risk !== null && parent?.riskClass !== f.risk) return false;
  if (
    f.origin !== null &&
    candidate.origin !== f.origin &&
    parent?.origin !== f.origin
  ) {
    return false;
  }
  return true;
}

export function distinctRepos(intents: BoardIntent[]): string[] {
  return [...new Set(intents.map((i) => i.repository).filter((r): r is string => r !== null))].sort();
}

export function distinctOrigins(intents: BoardIntent[], candidates: BoardCandidate[]): string[] {
  return [...new Set([...intents, ...candidates].map((x) => x.origin))]
    .filter((o) => ORIGIN_SET.has(o))
    .sort();
}

export interface BoardSection<T> {
  key: string;
  items: T[];
}

const STATE_ORDER: string[] = [...INTENT_STATES];
const RISK_ORDER: string[] = ['critical', 'high', 'medium', 'low'];

// Grouping is sectioned in canonical order (§7 density rules); empty sections
// are omitted rather than rendered as blank columns.
export function groupIntents(
  intents: BoardIntent[],
  mode: BoardGroupMode,
): Array<BoardSection<BoardIntent>> {
  const order = mode === 'state' ? STATE_ORDER : RISK_ORDER;
  return order
    .map((key) => ({ key, items: intents.filter((i) => (mode === 'state' ? i.state : i.riskClass) === key) }))
    .filter((section) => section.items.length > 0);
}
