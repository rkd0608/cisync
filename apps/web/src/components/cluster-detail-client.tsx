'use client';

import { useEffect, useState, type ReactElement } from 'react';
import Link from 'next/link';
import { ClusterGraph } from '@/components/cluster-graph';
import { EmptyState } from '@/components/empty-state';
import { ErrorState, type ApiErrorView } from '@/components/error-state';
import { NotFoundState } from '@/components/not-found-state';
import { SkeletonRows } from '@/components/skeleton';
import { StateBadge } from '@/components/state-badge';
import { getCluster, isNotFound } from '@/lib/cisync-api';
import type { Cluster } from '@/lib/api-schemas';

type DetailPhase =
  | { phase: 'loading' }
  | { phase: 'not_found' }
  | { phase: 'error'; error: ApiErrorView }
  | { phase: 'ready'; cluster: Cluster };

// WHY client-side data loading (B2 SSR fix): same rationale as the intent and
// candidate shells — SSR cannot resolve the relative gateway path, and any
// absolute fallback would pin a deployment topology into the bundle. Browser
// fetch through the same-origin proxy keeps host-config at zero.
export function ClusterDetailClient({ clusterId }: { clusterId: string }): ReactElement {
  const [detail, setDetail] = useState<DetailPhase>({ phase: 'loading' });

  useEffect(() => {
    let alive = true;
    void getCluster(clusterId).then((result) => {
      if (!alive) return;
      if (isNotFound(result)) {
        setDetail({ phase: 'not_found' });
        return;
      }
      if (!result.ok) {
        setDetail({
          phase: 'error',
          error: { code: result.code, message: result.message },
        });
        return;
      }
      setDetail({ phase: 'ready', cluster: result.data });
    });
    return () => {
      alive = false;
    };
  }, [clusterId]);

  switch (detail.phase) {
    case 'loading':
      return <SkeletonRows rows={3} />;
    case 'not_found':
      return <NotFoundState />;
    case 'error':
      return <ErrorState error={detail.error} />;
    case 'ready':
      return <ClusterDetailView cluster={detail.cluster} />;
  }
}

// Cluster view (mission Part 3): graph-lite representation up top, dense
// member table below for L2 drilling. Representative sorts first everywhere.
export function ClusterDetailView({ cluster }: { cluster: Cluster }): ReactElement {
  const members = [...cluster.members].sort((a, b) => {
    if (a.candidate_id === cluster.rep_candidate_id) return -1;
    if (b.candidate_id === cluster.rep_candidate_id) return 1;
    return a.candidate_id.localeCompare(b.candidate_id);
  });
  const graphMembers = members.map((member) => ({
    candidateId: member.candidate_id,
    relation: member.relation_to_rep,
    similarity: member.similarity_score,
    isRep: member.candidate_id === cluster.rep_candidate_id,
  }));

  return (
    <div className="route-rise flex flex-col gap-6">
      <header className="flex flex-col gap-2">
        <div className="flex flex-wrap items-center gap-2">
          <StateBadge state={cluster.state} />
          <span className="font-mono text-xs text-zinc-500">{cluster.repo}</span>
          <span className="font-mono text-[11px] text-zinc-400">
            strategy {cluster.strategy_version}
          </span>
          <span className="ml-auto font-mono text-xs text-zinc-400">{cluster.cluster_id}</span>
        </div>
        <h1 className="text-lg text-zinc-100">
          cluster · {cluster.member_count ?? members.length} member
          {(cluster.member_count ?? members.length) === 1 ? '' : 's'}
        </h1>
      </header>

      {members.length > 0 ? (
        <ClusterGraph repCandidateId={cluster.rep_candidate_id} members={graphMembers} />
      ) : (
        <EmptyState
          what="no members"
          whyEmpty="Clustering forms when ≥2 candidates overlap ≥0.6 path-similarity."
        />
      )}

      <section>
        <h2 className="mb-2 font-mono text-[11px] uppercase tracking-widest text-zinc-500">
          members · representative first
        </h2>
        {members.length === 0 ? null : (
          <ul className="flex flex-col gap-2">
            {members.map((member) => {
              const isRep = member.candidate_id === cluster.rep_candidate_id;
              return (
                <li
                  key={member.candidate_id}
                  className={`flex flex-wrap items-center gap-3 rounded-lg border px-4 py-2.5 font-mono text-xs transition-colors ${
                    isRep
                      ? 'border-[var(--color-accent)]/50 bg-[var(--color-accent)]/5'
                      : 'border-white/8 bg-[var(--color-surface)] hover:border-zinc-600'
                  }`}
                >
                  {isRep ? (
                    <span className="rounded-md bg-[var(--color-accent)]/20 px-1.5 py-0.5 text-[10px] uppercase tracking-widest text-[var(--color-accent-soft)]">
                      representative
                    </span>
                  ) : null}
                  <Link
                    href={`/candidates/${member.candidate_id}`}
                    className="text-zinc-200 hover:text-cyan-300"
                  >
                    {member.candidate_id.slice(0, 14)}…
                  </Link>
                  <span className="text-zinc-500">{member.relation_to_rep ?? ''}</span>
                  {member.similarity_score !== undefined ? (
                    <span className="ml-auto tabular-nums text-zinc-500">
                      similarity {(member.similarity_score * 100).toFixed(0)}%
                    </span>
                  ) : null}
                </li>
              );
            })}
          </ul>
        )}
      </section>
    </div>
  );
}
