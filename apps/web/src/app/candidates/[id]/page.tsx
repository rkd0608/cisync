import Link from 'next/link';
import { notFound } from 'next/navigation';
import { ContextChip } from '@/components/context-chip';
import { CopyEvidenceLink } from '@/components/copy-evidence-link';
import { DossierView } from '@/components/dossier-view';
import {
  InvalidatedEvidenceNotice,
  PinUnavailableNotice,
  PinnedDecisionChip,
  SupersededBanner,
  type FailedEvidenceItem,
} from '@/components/dossier-alerts';
import { DossierProvenanceFooter } from '@/components/dossier-provenance-footer';
import { EmptyState } from '@/components/empty-state';
import { ErrorState, type ApiErrorView } from '@/components/error-state';
import { ShadowBanner } from '@/components/shadow-banner';
import { StateBadge } from '@/components/state-badge';
import { shortSha } from '@/lib/format';
import { parsePermalinkParams } from '@/lib/permalink-params';
import { requireCandidateId } from '@/lib/route-guards';
import { getCandidate, getDossier, isNotFound } from '@/lib/cisync-api';

export const dynamic = 'force-dynamic';

type SearchParams = Record<string, string | string[] | undefined>;

// THE trust artifact (mission Part 3 flagship). Canonical URL is the
// permalink; ?at=dec_… pins the banner decision; provenance footer anchors
// the audit chain (T6).
export default async function CandidatePage({
  params,
  searchParams,
}: {
  params: Promise<{ id: string }>;
  searchParams: Promise<SearchParams>;
}): Promise<React.ReactElement> {
  const { id } = await params;
  requireCandidateId(id);
  const permalink = parsePermalinkParams(await searchParams);

  const [candidateResult, dossierResult] = await Promise.all([
    getCandidate(id),
    getDossier(id, permalink.pinnedDecisionId ?? undefined),
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

  // ?at pin state (frozen ruling #2): until G4 lands servers ignore the query
  // param and return latest — a pin that doesn't match renders the honest
  // mismatch notice, never a silently wrong "snapshot".
  const latestDecisionId = dossier?.decision.decision_id ?? null;
  const pinActive =
    permalink.pinnedDecisionId !== null && permalink.pinnedDecisionId === latestDecisionId;
  const pinUnavailable =
    permalink.pinnedDecisionId !== null && !pinActive;

  const superseded = candidate.state === 'superseded';
  const failedEvidence: FailedEvidenceItem[] =
    dossier?.evidence_accepted
      .filter((ev) => ev.verdict === 'fail')
      .map((ev) => ({
        evId: ev.ev_id,
        reason: typeof ev.meta?.reason === 'string' ? ev.meta.reason : undefined,
      })) ?? [];

  return (
    <div className="route-rise flex flex-col gap-6">
      <header className="flex flex-col gap-2">
        <div className="flex flex-wrap items-center gap-2">
          <StateBadge state={candidate.state} />
          {permalink.fromGithubCheck ? <ContextChip kind="github_check" /> : null}
          {pinActive && latestDecisionId !== null ? (
            <PinnedDecisionChip decisionId={latestDecisionId} />
          ) : null}
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
        {dossier !== null ? (
          // Share row doubles as the breadcrumb bar on this flagship page.
          <div className="flex items-center gap-3 border-t border-white/5 pt-3">
            <CopyEvidenceLink candidateId={candidate.candidate_id} decisionId={dossier.decision.decision_id} />
            <span className="font-mono text-[10px] uppercase tracking-widest text-zinc-600">
              permalink is safe for PRs &amp; slack — pinned to decision, never rewritten
            </span>
          </div>
        ) : null}
      </header>

      <ShadowBanner />

      {superseded ? <SupersededBanner clusterId={candidate.cluster_id} intentId={candidate.intent_id} /> : null}
      <InvalidatedEvidenceNotice items={failedEvidence} />
      {pinUnavailable ? <PinUnavailableNotice /> : null}

      <section>
        <h2 className="mb-3 font-mono text-[11px] uppercase tracking-widest text-zinc-500">
          evidence dossier
        </h2>
        {dossier !== null ? (
          <DossierView dossier={dossier} />
        ) : dossierError === null || isNotFound(dossierResult) ? (
          <EmptyState
            what="validation plan forming"
            whyEmpty="This candidate is submitted; its evidence dossier renders once a decision exists."
          />
        ) : (
          <ErrorState error={dossierError} />
        )}
      </section>

      {dossier !== null ? (
        <DossierProvenanceFooter
          inputsHash={dossier.inputs_hash}
          generatedAt={dossier.generated_at}
          candidateId={dossier.candidate_id}
          intentId={dossier.intent_id}
        />
      ) : null}
    </div>
  );
}
