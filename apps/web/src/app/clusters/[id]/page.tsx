import Link from 'next/link';
import { EmptyState } from '@/components/empty-state';
import { ErrorState, type ApiErrorView } from '@/components/error-state';
import { RelationBadge } from '@/components/relation-badge';
import { StateBadge } from '@/components/state-badge';
import { isNotFound } from '@/lib/sauron-api';
import { requireClusterId } from '@/lib/route-guards';
import { getCluster } from '@/lib/sauron-api';
import { notFound } from 'next/navigation';

export const dynamic = 'force-dynamic';

export default async function ClusterPage({
  params,
}: {
  params: Promise<{ id: string }>;
}): Promise<React.ReactElement> {
  const { id } = await params;
  requireClusterId(id);

  const result = await getCluster(id);
  if (isNotFound(result)) notFound();
  if (!result.ok) {
    const view: ApiErrorView = { code: result.code, message: result.message };
    return <ErrorState error={view} />;
  }

  const cluster = result.data;
  const members = [...cluster.members].sort((a, b) => {
    if (a.candidate_id === cluster.rep_candidate_id) return -1;
    if (b.candidate_id === cluster.rep_candidate_id) return 1;
    return a.candidate_id.localeCompare(b.candidate_id);
  });

  return (
    <div className="flex flex-col gap-6">
      <header className="flex flex-col gap-2">
        <div className="flex flex-wrap items-center gap-2">
          <StateBadge state={cluster.state} />
          <span className="font-mono text-xs text-zinc-500">{cluster.repo}</span>
          <span className="font-mono text-[11px] text-zinc-600">
            strategy {cluster.strategy_version}
          </span>
          <span className="ml-auto font-mono text-xs text-zinc-600">{cluster.cluster_id}</span>
        </div>
        <h1 className="text-lg text-zinc-100">
          cluster · {cluster.member_count ?? members.length} member
          {(cluster.member_count ?? members.length) === 1 ? '' : 's'}
        </h1>
      </header>

      <section>
        <h2 className="mb-2 font-mono text-[11px] uppercase tracking-widest text-zinc-500">
          members · representative first
        </h2>
        {members.length === 0 ? (
          <EmptyState title="no members" hint="Cluster has no live members." />
        ) : (
          <ul className="flex flex-col gap-2">
            {members.map((member) => {
              const isRep = member.candidate_id === cluster.rep_candidate_id;
              return (
                <li
                  key={member.candidate_id}
                  className={`flex flex-wrap items-center gap-3 rounded border px-4 py-2.5 font-mono text-xs ${
                    isRep
                      ? 'border-cyan-500/50 bg-cyan-500/5'
                      : 'border-zinc-800 bg-zinc-950'
                  }`}
                >
                  {isRep ? (
                    <span className="rounded bg-cyan-400/15 px-1.5 py-0.5 text-[10px] uppercase tracking-widest text-cyan-300">
                      representative
                    </span>
                  ) : null}
                  <Link
                    href={`/candidates/${member.candidate_id}`}
                    className="text-zinc-200 hover:text-cyan-300"
                  >
                    {member.candidate_id.slice(0, 14)}…
                  </Link>
                  <RelationBadge relation={isRep ? null : member.relation_to_rep} />
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
