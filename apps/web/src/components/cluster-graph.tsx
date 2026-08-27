import Link from 'next/link';
import type { ReactElement } from 'react';
import { truncateMiddle } from '@/lib/format';

const RELATION_EDGE_LABELS: Record<string, string> = {
  duplicate_of: '=',
  alternative_of: '<>',
  composable_with: '+',
  conflicts_with: '✕',
  prerequisite_of: '→',
};

interface MemberNode {
  candidateId: string;
  relation: string | null;
  similarity: number | undefined;
  isRep: boolean;
}

// Graph-lite (mission Part 3): representative node with member chips hung off
// a CSS connector rail — relation encoded by the edge glyph + label, never by
// color alone. Pure flexbox/borders; no canvas, no dependency.
export function ClusterGraph({
  repCandidateId,
  members,
}: {
  repCandidateId: string;
  members: MemberNode[];
}): ReactElement {
  const rep = members.find((member) => member.candidateId === repCandidateId);
  const others = members.filter((member) => !member.isRep);
  return (
    <div data-testid="cluster-graph" className="rounded-lg border border-white/8 bg-[var(--color-surface)] px-5 py-6">
      <p className="section-label">graph-lite · representative left</p>
      <div className="mt-4 flex items-stretch gap-4 overflow-x-auto pb-2">
        {/* Representative node */}
        {rep !== undefined ? (
          <Link
            href={`/candidates/${rep.candidateId}`}
            data-testid="node-representative"
            className="flex shrink-0 flex-col justify-center rounded-lg border border-[var(--color-accent)]/60 bg-[var(--color-accent)]/10 px-4 py-3 font-mono text-xs text-[var(--color-accent-soft)] transition-colors hover:bg-[var(--color-accent)]/20"
          >
            <span className="text-[10px] uppercase tracking-widest text-[var(--color-accent)]">representative</span>
            <span className="mt-1">{truncateMiddle(rep.candidateId, 10, 4)}</span>
          </Link>
        ) : null}
        {/* Edge rail + member nodes */}
        {others.length > 0 ? (
          <>
            <span aria-hidden className="my-auto h-px w-8 shrink-0 bg-zinc-700" />
            <ul className="flex shrink-0 flex-col gap-0">
              {others.map((member, index) => (
                <li key={member.candidateId} className={`flex items-center gap-2 ${index > 0 ? 'ml-8' : ''}`}>
                  <NodeChip member={member} />
                  {index < others.length - 1 ? (
                    <span aria-hidden className="absolute" />
                  ) : null}
                </li>
              ))}
            </ul>
          </>
        ) : (
          <p className="my-auto font-mono text-xs text-zinc-600">single-member cluster — no relations to draw</p>
        )}
      </div>
      <p className="mt-3 font-mono text-[10px] uppercase tracking-widest text-zinc-700">
        edges encode relation glyphs (= dup · &lt;&gt; alt · + comp · ✕ conflict · → prereq)
      </p>
    </div>
  );
}

function NodeChip({ member }: { member: MemberNode }): ReactElement {
  const edgeLabel = member.relation !== null ? (RELATION_EDGE_LABELS[member.relation] ?? '?') : '?';
  return (
    <>
      {/* The edge stub binds chip to rail with its relation label. */}
      <span aria-hidden className="relative inline-block">
        <span className="block h-px w-6 bg-zinc-700" />
        <span
          className="absolute -top-2.5 left-1/2 -translate-x-1/2 font-mono text-[10px] text-zinc-500"
          title={member.relation ?? 'related'}
        >
          {edgeLabel}
        </span>
      </span>
      <Link
        href={`/candidates/${member.candidateId}`}
        data-testid={`node-${member.candidateId}`}
        className="flex items-center gap-2 rounded-full border border-zinc-700 bg-[var(--color-surface-raised)] px-3 py-1.5 font-mono text-xs text-zinc-300 transition-colors hover:border-cyan-400/50 hover:text-cyan-300"
        title={`${member.relation ?? 'related'} · similarity ${member.similarity !== undefined ? `${Math.round(member.similarity * 100)}%` : 'unknown'}`}
      >
        {truncateMiddle(member.candidateId, 10, 4)}
        {member.similarity !== undefined ? (
          <span className="tabular-nums text-zinc-600">{Math.round(member.similarity * 100)}%</span>
        ) : null}
      </Link>
    </>
  );
}
