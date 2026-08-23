import Link from 'next/link';
import { DossierView } from '@/components/dossier-view';
import { EmptyState } from '@/components/empty-state';
import { ErrorState, type ApiErrorView } from '@/components/error-state';
import { StateBadge } from '@/components/state-badge';
import { isNotFound } from '@/lib/sauron-api';
import { shortSha } from '@/lib/format';
import { requireCandidateId } from '@/lib/route-guards';
import { getCandidate, getDossier } from '@/lib/sauron-api';
import { notFound } from 'next/navigation';

export const dynamic = 'force-dynamic';

export default async function CandidatePage({
  params,
}: {
  params: Promise<{ id: string }>;
}): Promise<React.ReactElement> {
  const { id } = await params;
  requireCandidateId(id);

  const [candidateResult, dossierResult] = await Promise.all([
    getCandidate(id),
    getDossier(id),
  ]);

  if (isNotFound(candidateResult)) notFound();
  if (!candidateResult.ok) {
    const view: ApiErrorView = { code: candidateResult.code, message: candidateResult.message };
    return <ErrorState error={view} />;
  }

  const candidate = candidateResult.data;
  const dossier = dossierResult.ok ? dossierResult.data : null;
  const dossierError: ApiErrorView | null = dossierResult.ok
    ? null
    : { code: dossierResult.code, message: dossierResult.message };

  return (
    <div className="flex flex-col gap-6">
      <header className="flex flex-col gap-2">
        <div className="flex flex-wrap items-center gap-2">
          <StateBadge state={candidate.state} />
          <span className="font-mono text-xs text-zinc-500">{shortSha(candidate.head_sha)}</span>
          <span className="ml-auto font-mono text-xs text-zinc-600">
            {candidate.candidate_id}
          </span>
        </div>
        <p className="font-mono text-xs text-zinc-500">
          intent{' '}
          <Link href={`/intents/${candidate.intent_id}`} className="text-cyan-400 hover:underline">
            {candidate.intent_id}
          </Link>
          {' · '}queue position {candidate.queue_position ?? '--'}
          {' · '}est. cost {(candidate.est_cost_millicents / 100_000).toFixed(2)} USD
        </p>
      </header>

      <section>
        <h2 className="mb-3 font-mono text-[11px] uppercase tracking-widest text-zinc-500">
          evidence dossier
        </h2>
        {dossier !== null ? (
          <DossierView dossier={dossier} />
        ) : dossierError === null || isNotFound(dossierResult) ? (
          <EmptyState
            title="no dossier yet"
            hint="A dossier is rendered once a decision exists for this candidate."
          />
        ) : (
          <ErrorState error={dossierError} />
        )}
      </section>
    </div>
  );
}
