'use client';

import { useEffect, useState, type ReactElement } from 'react';
import Link from 'next/link';
import { ConflictPanel } from '@/components/conflict-panel';
import { EmptyState } from '@/components/empty-state';
import { ErrorState, type ApiErrorView } from '@/components/error-state';
import { EvidenceBar } from '@/components/evidence-bar';
import { NotFoundState } from '@/components/not-found-state';
import { RiskPill } from '@/components/risk-pill';
import { StateBadge } from '@/components/state-badge';
import { StateLadderVertical } from '@/components/state-ladder-vertical';
import { SkeletonRows } from '@/components/skeleton';
import { HumanActionSlot } from '@/components/human-action-slot';
import { getIntent, isNotFound, listCandidates } from '@/lib/cisync-api';
import { formatCountdown } from '@/lib/format';
import type { Candidate, Intent } from '@/lib/api-schemas';

type DetailPhase =
  | { phase: 'loading' }
  | { phase: 'not_found' }
  | { phase: 'error'; error: ApiErrorView }
  | {
      phase: 'ready';
      intent: Intent;
      candidates: Candidate[];
      /** true when the listing call itself failed (honest empty pane). */
      candidatesListFailed?: boolean;
    };

// WHY client-side data loading (B2 SSR fix): the single-intent cockpit used to
// fetch on the server, but `fetch('/api/cisync/…')` has no URL base during
// SSR — it degraded every detail page to "control-plane unreachable". Any
// absolute server base would hardcode deployment topology (a loopback origin
// does not exist across Vercel lambdas; a public origin differs per host).
// Loading in the browser through the SAME same-origin gateway as the board
// keeps auth cookie + injected-bearer semantics identical everywhere with
// zero per-deployment env. Zod validation still gates every response
// (charter §2) inside lib/cisync-api.
export function IntentDetailClient({ intentId }: { intentId: string }): ReactElement {
  const [detail, setDetail] = useState<DetailPhase>({ phase: 'loading' });

  useEffect(() => {
    // `alive` guards against a race where a slow response for route A resolves
    // after the user navigated to route B (same mounted component).
    let alive = true;
    void Promise.all([getIntent(intentId), listCandidates(intentId)]).then(
      ([intentResult, candidatesResult]) => {
        if (!alive) return;
        if (isNotFound(intentResult)) {
          setDetail({ phase: 'not_found' });
          return;
        }
        if (!intentResult.ok) {
          setDetail({
            phase: 'error',
            error: { code: intentResult.code, message: intentResult.message },
          });
          return;
        }
        setDetail({
          phase: 'ready',
          intent: intentResult.data,
          // Candidate listing failure renders an honest empty pane below —
          // never partial-data fabrication.
          candidates: candidatesResult.ok ? candidatesResult.data : [],
          candidatesListFailed: !candidatesResult.ok,
        });
      },
    );
    return () => {
      alive = false;
    };
  }, [intentId]);

  switch (detail.phase) {
    case 'loading':
      return <SkeletonRows rows={3} />;
    case 'not_found':
      return <NotFoundState />;
    case 'error':
      return <ErrorState error={detail.error} />;
    case 'ready':
      return (
        <IntentDetailView
          intent={detail.intent}
          candidates={detail.candidates}
          candidatesListFailed={detail.candidatesListFailed ?? false}
        />
      );
  }
}

// Single-intent COCKPIT (mission Part 3): left rail = state ladder +
// envelope facts; right pane = work manifest (candidates, cost) and human
// action slots exactly at H-gated states — never dead controls.
export function IntentDetailView({
  intent,
  candidates,
  candidatesListFailed = false,
}: {
  intent: Intent;
  candidates: Candidate[];
  /** WHY per-call: the empty pane must say WHY it is empty (honesty rule). */
  candidatesListFailed?: boolean;
}): ReactElement {
  const countdown = formatCountdown(intent.deadline ?? null, Date.now());
  // Why sum over submitted rows only: declared-but-unsubmitted intents have
  // no priced work yet; the meter must not imply an estimate exists.
  const estCostUsd =
    candidates.reduce((sum, candidate) => sum + candidate.est_cost_millicents, 0) / 100_000;

  return (
    <div className="route-rise flex flex-col gap-6">
      <header className="flex flex-col gap-3">
        <div className="flex flex-wrap items-center gap-2">
          <StateBadge state={intent.state} />
          <RiskPill risk={intent.risk_class} />
          <span className="font-mono text-xs text-zinc-500">{countdown.label}</span>
          <span className="ml-auto font-mono text-xs text-zinc-400">{intent.intent_id}</span>
        </div>
        <h1 className="text-lg text-zinc-100">{intent.goal}</h1>
        <p className="font-mono text-xs text-zinc-500">
          {intent.repository} · created {intent.created_at}
          {intent.closed_at ? ` · closed ${intent.closed_at}` : ''}
        </p>
      </header>

      <div className="grid gap-6 lg:grid-cols-[280px_1fr]">
        <aside className="card-glass h-fit px-4 py-4">
          <p className="section-label">state ladder</p>
          <div className="mt-3">
            <StateLadderVertical state={intent.state} />
          </div>
          <div className="mt-5 border-t border-white/5 pt-4" data-testid="envelope-facts">
            <p className="section-label">envelope</p>
            <ul className="mt-2 flex flex-col gap-1.5 font-mono text-[11px] text-zinc-400">
              <li className="flex items-center justify-between gap-2">
                <span>evidence</span>
                <span className="w-32"><EvidenceBar pct={intent.evidence_completeness_pct} label="" /></span>
              </li>
              <li className="flex justify-between"><span>deadline</span><span>{countdown.tone === 'none' ? 'none' : countdown.label}</span></li>
              <li className="flex justify-between"><span>est. spend</span><span className="tabular-nums">{estCostUsd.toFixed(2)} USD</span></li>
            </ul>
            <p className="mt-3 section-label">owned surfaces</p>
            <ul className="mt-1.5 flex flex-wrap gap-1 font-mono text-[10px] text-zinc-300">
              {intent.owned_surfaces.map((surface) => (
                <li key={surface} className="rounded-md border border-zinc-800 px-1.5 py-0.5">{surface}</li>
              ))}
            </ul>
          </div>
        </aside>

        <section className="flex min-w-0 flex-col gap-5">
          {intent.state === 'blocked' ? <HumanActionSlot intentId={intent.intent_id} /> : null}

          <div data-testid="candidates-pane">
            <h2 className="mb-2 font-mono text-[11px] uppercase tracking-widest text-zinc-500">
              candidates ({candidates.length})
            </h2>
            {candidates.length === 0 ? (
              <EmptyState
                what="no candidates"
                whyEmpty={
                  candidatesListFailed
                    ? 'candidate listing failed; showing nothing rather than partial data'
                    : 'No candidate has been submitted against this intent yet.'
                }
              />
            ) : (
              <CandidatesTable candidates={candidates} />
            )}
          </div>

          <ConflictPanel conflicts={[]} />
        </section>
      </div>
    </div>
  );
}

function CandidatesTable({ candidates }: { candidates: Candidate[] }): ReactElement {
  return (
    <table className="w-full border-collapse font-mono text-xs">
      <thead>
        <tr className="border-b border-zinc-800 text-left text-[10px] uppercase tracking-widest text-zinc-400">
          <th className="py-1.5 pr-2">candidate</th>
          <th className="py-1.5 pr-2">state</th>
          <th className="py-1.5 pr-2">head sha</th>
          <th className="py-1.5 pr-2">cluster</th>
          <th className="py-1.5 pr-2 text-right">relation</th>
        </tr>
      </thead>
      <tbody>
        {candidates.map((candidate) => (
          <tr key={candidate.candidate_id} className="border-b border-zinc-900 transition-colors hover:bg-white/[0.03] last:border-0">
            <td className="py-1.5 pr-2">
              <Link href={`/candidates/${candidate.candidate_id}`} className="hover:text-[var(--color-accent-soft)]">
                {candidate.candidate_id.slice(0, 14)}…
              </Link>
            </td>
            <td className="py-1.5 pr-2"><StateBadge state={candidate.state} /></td>
            <td className="py-1.5 pr-2 text-zinc-400">{candidate.head_sha.slice(0, 8)}…</td>
            <td className="py-1.5 pr-2 text-zinc-400">
              {candidate.cluster_id ? (
                <Link href={`/clusters/${candidate.cluster_id}`} className="hover:text-cyan-300">{candidate.cluster_id.slice(0, 12)}…</Link>
              ) : ('--')}
            </td>
            <td className="py-1.5 pr-2 text-right">{candidate.relation_to_rep ?? '--'}</td>
          </tr>
        ))}
      </tbody>
    </table>
  );
}
