import Link from 'next/link';
import { ConflictPanel } from '@/components/conflict-panel';
import { EmptyState } from '@/components/empty-state';
import { ErrorState } from '@/components/error-state';
import { RelationBadge } from '@/components/relation-badge';
import { RiskPill } from '@/components/risk-pill';
import { StateBadge } from '@/components/state-badge';
import { EvidenceBar } from '@/components/evidence-bar';
import { StateMachineProgress } from '@/components/state-machine-progress';
import { isNotFound } from '@/lib/cisync-api';
import type { ApiErrorView } from '@/components/error-state';
import { formatCountdown } from '@/lib/format';
import { getIntent, listCandidates } from '@/lib/cisync-api';
import { requireIntentId } from '@/lib/route-guards';
import { notFound } from 'next/navigation';

export const dynamic = 'force-dynamic';

export default async function IntentDetailPage({
  params,
}: {
  params: Promise<{ id: string }>;
}): Promise<React.ReactElement> {
  const { id } = await params;
  requireIntentId(id);

  const [intentResult, candidatesResult] = await Promise.all([
    getIntent(id),
    listCandidates(id),
  ]);

  if (isNotFound(intentResult)) notFound();
  if (!intentResult.ok) {
    const view: ApiErrorView = {
      code: intentResult.code,
      message: intentResult.message,
    };
    return <ErrorState error={view} />;
  }

  const intent = intentResult.data;
  const candidates = candidatesResult.ok ? candidatesResult.data : [];
  const countdown = formatCountdown(intent.deadline ?? null, Date.now());

  return (
    <div className="flex flex-col gap-6">
      <header className="flex flex-col gap-3">
        <div className="flex flex-wrap items-center gap-2">
          <StateBadge state={intent.state} />
          <RiskPill risk={intent.risk_class} />
          <span className="font-mono text-xs text-zinc-500">{countdown.label}</span>
          <span className="ml-auto font-mono text-xs text-zinc-600">{intent.intent_id}</span>
        </div>
        <h1 className="text-lg text-zinc-100">{intent.goal}</h1>
        <p className="font-mono text-xs text-zinc-500">
          {intent.repository} · created {intent.created_at}
          {intent.closed_at ? ` · closed ${intent.closed_at}` : ''}
        </p>
        <StateMachineProgress state={intent.state} />
      </header>

      <section className="rounded border border-zinc-800 bg-zinc-950 px-4 py-3">
        <p className="font-mono text-[11px] uppercase tracking-widest text-zinc-500">
          evidence completeness (D8)
        </p>
        <div className="mt-2">
          <EvidenceBar pct={intent.evidence_completeness_pct} />
        </div>
      </section>

      <section>
        <h2 className="mb-2 font-mono text-[11px] uppercase tracking-widest text-zinc-500">
          owned surfaces
        </h2>
        <ul className="flex flex-wrap gap-1.5 font-mono text-xs text-zinc-300">
          {intent.owned_surfaces.map((surface) => (
            <li key={surface} className="rounded border border-zinc-800 px-2 py-0.5">
              {surface}
            </li>
          ))}
        </ul>
      </section>

      <ConflictPanel conflicts={[]} />

      <section>
        <h2 className="mb-2 font-mono text-[11px] uppercase tracking-widest text-zinc-500">
          candidates ({candidates.length})
        </h2>
        {candidates.length === 0 ? (
          <EmptyState
            what="no candidates"
            whyEmpty={
              candidatesResult.ok
                ? 'No candidate has been submitted against this intent yet.'
                : 'candidate listing failed; showing nothing rather than partial data'
            }
          />
        ) : (
          <table className="w-full border-collapse font-mono text-xs">
            <thead>
              <tr className="border-b border-zinc-800 text-left text-[10px] uppercase tracking-widest text-zinc-600">
                <th className="py-1.5 pr-2">candidate</th>
                <th className="py-1.5 pr-2">state</th>
                <th className="py-1.5 pr-2">head sha</th>
                <th className="py-1.5 pr-2">cluster</th>
                <th className="py-1.5">relation</th>
              </tr>
            </thead>
            <tbody>
              {candidates.map((candidate) => (
                <tr key={candidate.candidate_id} className="border-b border-zinc-900 last:border-0">
                  <td className="py-1.5 pr-2">
                    <Link href={`/candidates/${candidate.candidate_id}`} className="hover:text-cyan-300">
                      {candidate.candidate_id.slice(0, 14)}…
                    </Link>
                  </td>
                  <td className="py-1.5 pr-2">
                    <StateBadge state={candidate.state} />
                  </td>
                  <td className="py-1.5 pr-2 text-zinc-400">{candidate.head_sha}</td>
                  <td className="py-1.5 pr-2 text-zinc-400">
                    {candidate.cluster_id ? (
                      <Link href={`/clusters/${candidate.cluster_id}`} className="hover:text-cyan-300">
                        {candidate.cluster_id.slice(0, 12)}…
                      </Link>
                    ) : (
                      '--'
                    )}
                  </td>
                  <td className="py-1.5">
                    <RelationBadge relation={candidate.relation_to_rep} />
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </section>
    </div>
  );
}
