import { eventsTailSchema, type EventsTail, type LedgerEvent } from './api-schemas.js';
import { harnessEnv } from './env.js';
import { authHeaders, request } from './http.js';

/**
 * Ledger tail readers for live probes. All reads follow next_seq so results
 * are position-based, never page-position-based: suites share one stack and
 * the global seq advances continuously, so single-page or re-drain-from-zero
 * strategies either miss fresh events (first-page-only) or livelock against
 * the append rate (O(ledger) per poll).
 *
 * WHY bounded lookbacks: waiters/pollers only ever need events created shortly
 * before they start; scanning from seq 0 costs O(entire ledger) per test on a
 * shared stack whose ledger grows monotonically across suite runs.
 */

function authed(): Record<string, string> {
  return authHeaders(harnessEnv().adminToken);
}

/** Default recent-history window for waiters/pollers (in ledger events). */
export const DEFAULT_LOOKBACK = 4000;

/** Fetch one page of the tail; most callers want scanTail/findEvents. */
export async function tailEvents(
  apiBase: string,
  afterSeq = 0,
  types?: string[],
  limit = 500,
): Promise<EventsTail> {
  const params = new URLSearchParams({ after_seq: String(afterSeq), limit: String(limit) });
  if (types?.length) params.set('types', types.join(','));
  const res = await request({ url: `${apiBase}/events?${params}`, method: 'GET', headers: authed() }, eventsTailSchema);
  if (!res.ok) throw new Error(`tailEvents failed: ${JSON.stringify(res.body)}`);
  return res.body;
}

/** Current head seq (next unseen); cheap page-beyond-end probe. */
export async function headSeq(apiBase: string): Promise<number> {
  const page = await tailEvents(apiBase, Number.MAX_SAFE_INTEGER, undefined, 1);
  return page.next_seq;
}

/** Starting cursor for a recent-history scan: head minus the lookback. */
export async function recentStart(apiBase: string, lookback = DEFAULT_LOOKBACK): Promise<number> {
  const head = await headSeq(apiBase);
  return Math.max(0, head - lookback);
}

/** One forward sweep of the tail from `cursor` until caught up (or bounded). */
export interface TailScan {
  events: LedgerEvent[];
  /** Cursor to resume from (next unseen seq). */
  cursor: number;
  /** False when the page/page-count bound hit before reaching head. */
  caughtUp: boolean;
}

/**
 * Sweep the tail FORWARD from `cursor`, following next_seq. Bounded so a
 * stuck or regressing cursor cannot loop forever.
 */
export async function scanTail(
  apiBase: string,
  cursor = 0,
  opts: { types?: string[]; pageSize?: number; maxPages?: number } = {},
): Promise<TailScan> {
  const pageSize = opts.pageSize ?? 500;
  const maxPages = opts.maxPages ?? 40;
  const all: LedgerEvent[] = [];
  let position = cursor;
  for (let page = 0; page < maxPages; page++) {
    const batch = await tailEvents(apiBase, position, opts.types, pageSize);
    all.push(...batch.events);
    if (batch.next_seq <= position) return { events: all, cursor: position, caughtUp: true };
    position = batch.next_seq;
    if (batch.events.length < pageSize) return { events: all, cursor: position, caughtUp: true };
  }
  return { events: all, cursor: position, caughtUp: false };
}

/**
 * Drain the whole ledger tail exactly once (segmented resumable sweeps).
 * Full history: only use when older events are genuinely required.
 */
export async function drainEvents(apiBase: string, maxSegments = 10): Promise<LedgerEvent[]> {
  let cursor = 0;
  const all: LedgerEvent[] = [];
  for (let segment = 0; segment < maxSegments; segment++) {
    const scan = await scanTail(apiBase, cursor);
    all.push(...scan.events);
    cursor = scan.cursor;
    if (scan.caughtUp) return all;
  }
  throw new Error(`drainEvents failed to catch up after ${maxSegments} segments`);
}

/**
 * Recent history snapshot: the last `lookback` events, one segmented sweep.
 * WHAT the dossier-style probes need (their own candidates' events are always
 * seconds old) without paying full-ledger cost on every invocation.
 */
export async function recentEvents(apiBase: string, lookback = DEFAULT_LOOKBACK): Promise<LedgerEvent[]> {
  const start = await recentStart(apiBase, lookback);
  const scan = await scanTail(apiBase, start, { maxPages: 40 + Math.ceil(lookback / 500) });
  return scan.events;
}

/** Poll the tail INCREMENTALLY until at least one event matches; [] on timeout. */
export async function findEvents(
  apiBase: string,
  matches: (ev: LedgerEvent) => boolean,
  opts: { timeoutMs?: number; pollMs?: number; lookback?: number },
): Promise<LedgerEvent[]> {
  const deadline = Date.now() + (opts.timeoutMs ?? 15_000);
  let cursor = await recentStart(apiBase, opts.lookback);
  let matched: LedgerEvent[] = [];
  while (Date.now() < deadline) {
    const scan = await scanTail(apiBase, cursor);
    for (const ev of scan.events) {
      if (matches(ev)) matched.push(ev);
    }
    if (matched.length > 0) return matched;
    cursor = scan.cursor;
    await new Promise((r) => setTimeout(r, opts.pollMs ?? 300));
  }
  return matched;
}
