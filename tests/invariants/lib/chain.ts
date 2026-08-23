import { createHash } from 'node:crypto';

/**
 * Mirror of control-plane internal/domain/event.go ComputeEntryHash —
 * entry_hash = sha256:hex(sha256("seq|id|type|version|payload_bare|prev_bare")).
 * WHY duplicated here: I-07 verification must be independent of the service
 * under test; if the two implementations drift, fixtures fail loudly.
 */

export const HASH_PREFIX = 'sha256:';
const BARE_LEN = 64;

function bareHash(value: string): string {
  return value.startsWith(HASH_PREFIX) && value.length === HASH_PREFIX.length + BARE_LEN
    ? value.slice(HASH_PREFIX.length)
    : value;
}

export function computeEntryHash(
  seq: number,
  id: string,
  eventType: string,
  version: number,
  payloadSha256: string,
  prevHash: string,
): string {
  const material = `${seq}|${id}|${eventType}|${version}|${bareHash(payloadSha256)}|${bareHash(prevHash)}`;
  return HASH_PREFIX + createHash('sha256').update(material, 'utf8').digest('hex');
}

/** Go json.Marshal sorts map keys; replicate so payload digests match. */
export function canonicalJson(value: unknown): string {
  if (value === null || typeof value !== 'object') return JSON.stringify(value);
  if (Array.isArray(value)) return `[${value.map(canonicalJson).join(',')}]`;
  const keys = Object.keys(value).sort();
  const pairs = keys.map((k) => `${JSON.stringify(k)}:${canonicalJson((value as Record<string, unknown>)[k])}`);
  return `{${pairs.join(',')}}`;
}

export function hashPayload(payload: Record<string, unknown>): string {
  return HASH_PREFIX + createHash('sha256').update(canonicalJson(payload), 'utf8').digest('hex');
}

export interface ChainReport {
  ok: boolean;
  entriesChecked: number;
  failure?: { seq: number; rule: string };
}

/** Structural superset accepted by verifyChain (zod envelopes, builders, fixtures). */
export interface ChainLink {
  readonly seq: number;
  readonly id: string;
  readonly type: string;
  readonly version: number;
  readonly payload_sha256: string;
  readonly prev_hash: string;
  readonly entry_hash: string;
  readonly payload: Record<string, unknown>;
}

/**
 * Full structural + cryptographic chain verification over a seq-ordered
 * page of envelopes (I-07): payload digest, entry hash recompute,
 * prev_hash linkage, contiguous sequence numbers.
 */
export function verifyChain(events: readonly ChainLink[], expectedFirstSeq?: number): ChainReport {
  let expectPrev = '';
  for (let i = 0; i < events.length; i++) {
    const ev = events[i];
    if (!ev) return { ok: false, entriesChecked: i, failure: { seq: i, rule: 'missing element' } };
    const at = ev.seq;
    if (ev.version !== 1) return { ok: false, entriesChecked: i, failure: { seq: at, rule: 'version != 1' } };
    if (hashPayload(ev.payload) !== ev.payload_sha256) {
      return { ok: false, entriesChecked: i, failure: { seq: at, rule: 'payload_sha256 mismatch' } };
    }
    if (computeEntryHash(ev.seq, ev.id, ev.type, ev.version, ev.payload_sha256, ev.prev_hash) !== ev.entry_hash) {
      return { ok: false, entriesChecked: i, failure: { seq: at, rule: 'entry_hash mismatch' } };
    }
    if (expectPrev !== '' && ev.prev_hash !== expectPrev) {
      return { ok: false, entriesChecked: i, failure: { seq: at, rule: 'prev_hash does not chain' } };
    }
    if (i > 0) {
      const before = events[i - 1];
      if (before && ev.seq !== before.seq + 1) {
        return { ok: false, entriesChecked: i, failure: { seq: at, rule: 'seq gap (event missing)' } };
      }
    } else if (expectedFirstSeq !== undefined && ev.seq !== expectedFirstSeq) {
      return { ok: false, entriesChecked: i, failure: { seq: at, rule: 'unexpected first seq' } };
    }
    expectPrev = ev.entry_hash;
  }
  return { ok: true, entriesChecked: events.length };
}
