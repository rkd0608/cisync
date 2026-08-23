/**
 * Documented acceptance rules re-encoded from docs/INVARIANTS.md and
 * services/control-plane/internal/evidence (acceptance precedence).
 * WHY mirrored here: contract-mode suites prove the DOCUMENTED rules reject
 * attack vectors even while enforcement still lives server-side; the same
 * vectors become live-mode e2e inputs.
 */

export const SKIP_OUTCOMES = ['skipped', 'quarantined', 'filtered'] as const;
export const EXECUTED_OUTCOMES = ['passed', 'failed'] as const;
export type RunnerOutcome = (typeof SKIP_OUTCOMES)[number] | (typeof EXECUTED_OUTCOMES)[number] | string;

export interface ProposedEvidenceRecord {
  recordId: string;
  runId: string;
  attempt: number;
  kind: string;
  verdict: 'pass' | 'fail' | string;
  outcome: RunnerOutcome;
  digests: readonly string[];
  inputsHash: string;
  leaseJti: string;
}

export interface AcceptanceContext {
  expectedLeaseJti: string;
  expectedInputsHash: string;
  acceptedRefs: readonly { runId: string; attempt: number; leaseJti: string }[];
}

export type EvidenceAction = 'accept' | 'reject' | 'quarantine';

const SHA256_RE = /^sha256:[a-f0-9]{64}$/;

function structuralDefect(p: ProposedEvidenceRecord): boolean {
  return (
    p.recordId === '' || p.runId === '' || p.kind === '' || p.attempt < 1 ||
    (p.verdict !== 'pass' && p.verdict !== 'fail') ||
    p.digests.some((d) => !SHA256_RE.test(d))
  );
}

/** Deterministic ruling; check order is the documented precedence. */
export function evaluateEvidenceRecord(p: ProposedEvidenceRecord, ctx: AcceptanceContext): { action: EvidenceAction; reason: string } {
  if (structuralDefect(p)) return { action: 'reject', reason: 'malformed_record' };
  if (ctx.expectedLeaseJti === '' || p.leaseJti !== ctx.expectedLeaseJti) {
    return { action: 'reject', reason: 'lease_provenance_mismatch' };
  }
  if (ctx.expectedInputsHash === '' || p.inputsHash !== ctx.expectedInputsHash) {
    return { action: 'reject', reason: 'inputs_hash_mismatch' };
  }
  if (ctx.acceptedRefs.some((r) => r.runId === p.runId && r.attempt === p.attempt)) {
    return { action: 'reject', reason: 'duplicate_run_attempt' };
  }
  if (p.leaseJti !== '' && ctx.acceptedRefs.some((r) => r.leaseJti === p.leaseJti)) {
    return { action: 'reject', reason: 'lease_jti_already_accepted' };
  }
  if ((SKIP_OUTCOMES as readonly string[]).includes(p.outcome)) {
    return { action: 'reject', reason: 'skip_quarantine_never_positive_evidence' };
  }
  if (p.outcome === 'passed' && p.verdict !== 'pass') return { action: 'reject', reason: 'verdict_not_supported_by_outcome' };
  if (p.outcome === 'failed' && p.verdict !== 'fail') return { action: 'reject', reason: 'verdict_not_supported_by_outcome' };
  if (!(EXECUTED_OUTCOMES as readonly string[]).includes(p.outcome)) {
    return { action: 'reject', reason: 'verdict_not_supported_by_outcome' };
  }
  return { action: 'accept', reason: '' };
}

/**
 * I-13 deterministic tie-break comparator: priority desc → age asc →
 * ULID asc. Mirrors scheduler ordering docs; Go owns live enforcement.
 */
export interface RankedRunLike {
  priorityScore: number;
  createdSeq: number;
  id: string;
}

export function compareRanked(a: RankedRunLike, b: RankedRunLike): number {
  if (a.priorityScore !== b.priorityScore) return b.priorityScore - a.priorityScore;
  if (a.createdSeq !== b.createdSeq) return a.createdSeq - b.createdSeq;
  if (a.id !== b.id) return a.id < b.id ? -1 : 1;
  return 0;
}
