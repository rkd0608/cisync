import { computeEntryHash, hashPayload } from './chain.js';

/**
 * Deterministic builders for valid ledger envelopes/payloads used by
 * contract-mode suites, golden fixtures and attack-vector generators.
 * WHY deterministic: property tests must be reproducible and fixture files
 * must carry real, internally-consistent hashes.
 */

const CROCKFORD = '0123456789ABCDEFGHJKMNPQRSTVWXYZ'; // ULID alphabet (no I/L/O/U)
export const GENESIS_PREV = 'sha256:' + '0'.repeat(64);

/** Stable pseudo-ULID: 26 Crockford chars derived from the seed text. */
export function fakeUlid(seed: string): string {
  let out = '';
  for (let i = 0; i < 26; i++) {
    const acc = ((seed.charCodeAt(i % seed.length) + i * 31) * 1103515245 + 12345) >>> 0;
    out += CROCKFORD[acc % 32];
  }
  return out;
}

export type IdPrefix =
  | 'evt' | 'int' | 'cand' | 'clus' | 'val' | 'run' | 'ev' | 'fc'
  | 'lease' | 'repair' | 'pol' | 'dec' | 'org' | 'corr';

export function prefixedId(prefix: IdPrefix, seed: string): string {
  return `${prefix}_${fakeUlid(`${prefix}:${seed}`)}`;
}

export const TEST_TENANT = prefixedId('org', 'tenant-alpha');
export const OTHER_TENANT = prefixedId('org', 'tenant-beta');

export type AggregateType =
  | 'delivery' | 'intent' | 'lease' | 'candidate' | 'cluster' | 'validation_plan'
  | 'validation_run' | 'evidence' | 'failure_case' | 'repair_task' | 'policy' | 'decision';

export interface EventSpec {
  seq: number;
  type: string;
  aggregate: { type: AggregateType; id: string };
  tenantId?: string;
  correlationId?: string;
  actorKind?: 'agent' | 'human' | 'service' | 'github';
  payload: Record<string, unknown>;
}

/** Structurally identical to LedgerEvent; kept loose so vectors can tamper. */
export interface EnvelopeLike {
  id: string;
  seq: number;
  type: string;
  version: number;
  tenant_id: string;
  aggregate: { type: string; id: string };
  causation_id: string;
  correlation_id: string;
  actor: { kind: string; id: string };
  occurred_at: string;
  payload_sha256: string;
  prev_hash: string;
  entry_hash: string;
  payload: Record<string, unknown>;
}

export function buildEnvelope(spec: EventSpec, prevHash: string, seqOverride?: number): EnvelopeLike {
  const seq = seqOverride ?? spec.seq;
  const payloadSha256 = hashPayload(spec.payload);
  const id = prefixedId('evt', `${spec.type}@${seq}`);
  return {
    id,
    seq,
    type: spec.type,
    version: 1,
    tenant_id: spec.tenantId ?? TEST_TENANT,
    aggregate: { ...spec.aggregate },
    causation_id: prefixedId('evt', `cause:${spec.type}@${seq}`),
    correlation_id: spec.correlationId ?? prefixedId('corr', `corr:${spec.type}@${seq}`),
    actor: { kind: spec.actorKind ?? 'service', id: 'cisync-test' },
    occurred_at: '2026-08-23T00:00:00.000Z',
    payload_sha256: payloadSha256,
    prev_hash: prevHash,
    entry_hash: computeEntryHash(seq, id, spec.type, 1, payloadSha256, prevHash),
    payload: spec.payload,
  };
}

/** Chain a list of specs into internally consistent envelopes. */
export function buildChainedEnvelopes(specs: readonly EventSpec[]): EnvelopeLike[] {
  const out: EnvelopeLike[] = [];
  let prev = GENESIS_PREV;
  for (const spec of specs) {
    const env = buildEnvelope(spec, prev);
    out.push(env);
    prev = env.entry_hash;
  }
  return out;
}

/** Indexing under noUncheckedIndexedAccess needs this everywhere suites build single envelopes. */
export function firstEnvelope(envelopes: readonly EnvelopeLike[]): EnvelopeLike {
  const first = envelopes[0];
  if (!first) throw new Error('envelope builder produced an empty chain');
  return first;
}
