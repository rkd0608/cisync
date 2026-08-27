'use client';

import { useEffect, useState, type ReactElement } from 'react';
import Link from 'next/link';
import { useSearchParams } from 'next/navigation';
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
import { NotFoundState } from '@/components/not-found-state';
import { ShadowBanner } from '@/components/shadow-banner';
import { SkeletonRows } from '@/components/skeleton';
import { StateBadge } from '@/components/state-badge';
import { shortSha } from '@/lib/format';
import { parsePermalinkParams, urlSearchParamsToRecord } from '@/lib/permalink-params';
import { getCandidate, getDossier, isNotFound } from '@/lib/cisync-api';
import type { Candidate, EvidenceDossier } from '@/lib/api-schemas';

type DetailPhase =
  | { phase: 'loading' }
  | { phase: 'not_found' }
  | { phase: 'error'; error: ApiErrorView }
  | {
      phase: 'ready';
      candidate: Candidate;
      dossier: EvidenceDossier | null;
      dossierError: ApiErrorView | null;
      pinnedDecisionId: string | null;
      fromGithubCheck: boolean;
    };

// WHY client-side data loading (B2 SSR fix): server-era fetch of the relative
// gateway path has no URL base during SSR. The browser shell keeps the
// same-origin /api/cisync semantics (cookie rides along, bearer injected by
// the route handler) on any host with zero deployment env.
export function CandidateDetailClient({ candidateId }: { candidateId: string }): ReactElement {
  const [detail, setDetail] = useState<DetailPhase>({ phase: 'loading' });
  // ?at=…&src=… permalink pins arrive via the URL; read them browser-side and
  // funnel through the same hardened parser the server page used.
  const searchParams = useSearchParams();
  const permalink = parsePermalinkParams(urlSearchParamsToRecord(searchParams));

  useEffect(() => {
    let alive = true;
    void Promise.all([
      getCandidate(candidateId),
      getDossier(candidateId, permalink.pinnedDecisionId ?? undefined),
    ]).then(([candidateResult, dossierResult]) => {
      if (!alive) return;
      if (isNotFound(candidateResult)) {
        setDetail({ phase: 'not_found' });
        return;
      }
      if (!candidateResult.ok) {
        setDetail({
          phase: 'error',
          error: { code: candidateResult.code, message: candidateResult.message },
        });
        return;
      }
      setDetail({
        phase: 'ready',
        candidate: candidateResult.data,
        dossier: dossierResult.ok ? dossierResult.data : null,
        dossierError: dossierResult.ok
          ? null
          : { code: dossierResult.code, message: dossierResult.message },
        pinnedDecisionId: permalink.pinnedDecisionId,
        fromGithubCheck: permalink.fromGithubCheck,
      });
    });
    return () => {
      alive = false;
    };
    // Recompute only when identity inputs change; permalink object is derived
    // fresh each render but stays value-stable for the same URL.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [candidateId, permalink.pinnedDecisionId, permalink.fromGithubCheck]);

  switch (detail.phase) {
    case 'loading':
      return <SkeletonRows rows={3} />;
    case 'not_found':
      return <NotFoundState />;
    case 'error':
      return <ErrorState error={detail.error} />;
    case 'ready':
      return (
        <CandidateDetailView
          candidate={detail.candidate}
          dossier={detail.dossier}
          dossierError={detail.dossierError}
          pinnedDecisionId={detail.pinnedDecisionId}
          fromGithubCheck={detail.fromGithubCheck}
        />
      );
  }
}

// THE trust artifact (mission Part 3 flagship). Canonical URL is the
// permalink; ?at=dec_… pins the banner decision; provenance footer anchors
// the audit chain (T6).
export function CandidateDetailView({
  candidate,
  dossier,
  dossierError,
  pinnedDecisionId,
  fromGithubCheck,
}: {
  candidate: Candidate;
  dossier: EvidenceDossier | null;
  dossierError: ApiErrorView | null;
  pinnedDecisionId: string | null;
  fromGithubCheck: boolean;
}): ReactElement {
  // ?at pin state (frozen ruling #2): until G4 lands servers ignore the query
  // param and return latest — a pin that doesn't match renders the honest
  // mismatch notice, never a silently wrong "snapshot".
  const latestDecisionId = dossier?.decision.decision_id ?? null;
  const pinActive =
    pinnedDecisionId !== null && pinnedDecisionId === latestDecisionId;
  const pinUnavailable =
    pinnedDecisionId !== null && !pinActive;

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
          {fromGithubCheck ? <ContextChip kind="github_check" /> : null}
          {pinActive && latestDecisionId !== null ? (
            <PinnedDecisionChip decisionId={latestDecisionId} />
          ) : null}
          <span className="font-mono text-xs text-zinc-500">{shortSha(candidate.head_sha)}</span>
          <span className="ml-auto font-mono text-xs text-zinc-400">
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
            <span className="font-mono text-[10px] uppercase tracking-widest text-zinc-400">
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
        ) : dossierError === null || isNotFoundError(dossierError) ? (
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

function isNotFoundError(error: ApiErrorView): boolean {
  // Uniform 404: a cross-tenant decision id is indistinguishable from a
  // nonexistent one — the dossier section reads "forming", never an error.
  return error.code === 'not_found';
}
