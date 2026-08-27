import type { ReactElement } from 'react';
import type { EvidenceDossier } from '@/lib/api-schemas';
import { DecisionBanner } from './decision-banner';
import { IdLabel } from './id-label';

function MetaList({ meta }: { meta: Record<string, unknown> }): ReactElement | null {
  const entries = Object.entries(meta);
  if (entries.length === 0) return null;
  return (
    <span className="text-zinc-500">
      {' '}
      · {entries.map(([key, value]) => `${key}=${String(value)}`).join(', ')}
    </span>
  );
}

// §7 evidence glyphs, one meaning per glyph, never reused:
// ✓ accepted · ✕ failed · ○ deferred/skipped · ⟳ running · ⏹ superseded.

// Full §7 dossier render (mission Part 3 flagship): sticky decision banner,
// accordion evidence groups styled distinctly per section. Deferred /
// uncertainty / post-merge headings ALWAYS render with their count so silence
// is never mistaken for absence of risk.
export function DossierView({ dossier }: { dossier: EvidenceDossier }): ReactElement {
  const policyLabel =
    dossier.decision.policy?.policy_id !== undefined
      ? `${dossier.decision.policy.policy_id} v${dossier.decision.policy?.version ?? '?'}`
      : 'unresolved policy';
  const failedAccepted = dossier.evidence_accepted.filter((ev) => ev.verdict === 'fail').length;
  return (
    <article className="flex flex-col gap-4" data-testid="dossier-view">
      {/* Sticky: the verdict stays pinned while long evidence scrolls under it. */}
      <div className="sticky top-[57px] z-20 -mx-1 px-1 py-1 backdrop-blur-md" data-testid="sticky-decision">
        <DecisionBanner decision={dossier.decision} />
        <p className="-mt-1 px-1 font-mono text-[11px] text-zinc-500">
          inputs_hash {dossier.inputs_hash} · generated {dossier.generated_at}
        </p>
      </div>

      <details open className="card-glass group px-4 py-4" data-testid="evidence-accepted-group">
        <summary className="cursor-pointer select-none list-none">
          <h3 className={`font-mono text-[11px] uppercase tracking-widest ${failedAccepted > 0 ? 'text-[var(--color-risk-critical)]' : 'text-emerald-400'}`}>
            ✓ accepted evidence ({dossier.evidence_accepted.length})
            {failedAccepted > 0 ? ` · ✕ ${failedAccepted} later failed` : ''}
          </h3>
        </summary>
        {dossier.evidence_accepted.length === 0 ? (
          <p className="mt-2 text-xs text-zinc-600">none recorded</p>
        ) : (
          <ul className="mt-2 flex flex-col gap-1 font-mono text-xs">
            {dossier.evidence_accepted.map((ev) => (
              <li key={ev.ev_id} className="flex flex-wrap items-baseline gap-2">
                <span className={ev.verdict === 'pass' ? 'text-emerald-300' : 'text-[var(--color-risk-critical)]'}>
                  {ev.verdict === 'pass' ? '✓' : '✕'}
                </span>
                <span className="text-zinc-100">{ev.kind}</span>
                <IdLabel id={ev.ev_id} />
                <MetaList meta={ev.meta ?? {}} />
              </li>
            ))}
          </ul>
        )}
      </details>

      <details className="rounded-lg border border-amber-900/50 bg-amber-950/10 group px-4 py-4" data-testid="evidence-deferred-group">
        <summary className="cursor-pointer select-none list-none">
          <h3 className="font-mono text-[11px] uppercase tracking-widest text-amber-400">
            ○ deferred evidence — with reasons ({dossier.evidence_deferred.length})
          </h3>
        </summary>
        <DeferredBody items={dossier.evidence_deferred} policyLabel={policyLabel} />
      </details>

      <div className="grid gap-4 md:grid-cols-2">
        <details className="card-glass px-4 py-4" data-testid="uncertainty-group">
          <summary className="cursor-pointer select-none list-none">
            <h3 className="font-mono text-[11px] uppercase tracking-widest text-sky-400">
              known uncertainty ({dossier.known_uncertainty.length})
            </h3>
          </summary>
          <UncertaintyList items={dossier.known_uncertainty} />
        </details>
        <details className="rounded-lg border border-violet-900/50 bg-violet-950/10 px-4 py-4" data-testid="post-merge-group">
          <summary className="cursor-pointer select-none list-none">
            <h3 className="font-mono text-[11px] uppercase tracking-widest text-violet-400">
              required post-merge ({dossier.required_post_merge.length})
            </h3>
          </summary>
          <PostMergeList items={dossier.required_post_merge} />
        </details>
      </div>
    </article>
  );
}

type DeferredItem = EvidenceDossier['evidence_deferred'][number];
type UncertaintyItem = EvidenceDossier['known_uncertainty'][number];
type PostMergeEntry = EvidenceDossier['required_post_merge'][number];

function DeferredBody({ items, policyLabel }: { items: DeferredItem[]; policyLabel: string }): ReactElement {
  if (items.length === 0) {
    // §2.8: absence of deferral is information — cite the policy that ran
    // everything required, never a blank section.
    return (
      <p data-testid="deferred-empty" className="mt-2 text-xs text-zinc-600">
        Nothing deferred — plan ran everything required by {policyLabel}.
      </p>
    );
  }
  return (
    <ul className="mt-2 flex flex-col gap-2 text-xs">
      {items.map((item) => (
        <li key={item.kind}>
          <span aria-hidden className="mr-1 text-amber-300">○</span>
          <span className="font-mono text-amber-200">{item.kind}</span>
          <span className="ml-2 text-zinc-300">{item.reason}</span>
          <span className="ml-2 font-mono text-zinc-500">stage: {item.stage_required}</span>
        </li>
      ))}
    </ul>
  );
}

// T3: uncertainty is content — neutral informational blocks with mitigation
// text; reserved for things a human would ask about.
function UncertaintyList({ items }: { items: UncertaintyItem[] }): ReactElement {
  if (items.length === 0) return <p className="mt-2 text-xs text-zinc-600">none declared</p>;
  return (
    <ul className="mt-2 flex flex-col gap-2 text-xs">
      {items.map((item, index) => (
        <li key={index}>
          <p className="text-zinc-300">{item.description ?? '(undescribed)'}</p>
          {item.mitigation ? (
            <p className="text-zinc-500">mitigation: {item.mitigation}</p>
          ) : null}
        </li>
      ))}
    </ul>
  );
}

function PostMergeList({ items }: { items: PostMergeEntry[] }): ReactElement {
  if (items.length === 0) return <p className="mt-2 text-xs text-zinc-600">none required</p>;
  return (
    <ul className="mt-2 flex flex-col gap-1 font-mono text-xs">
      {items.map((item, index) => (
        <li key={index} className="flex flex-wrap items-baseline gap-2">
          <span className="text-violet-300">{item.kind ?? '?'}</span>
          <ParamsInline params={item.params ?? {}} />
        </li>
      ))}
    </ul>
  );
}

function ParamsInline({ params }: { params: Record<string, unknown> }): ReactElement | null {
  const entries = Object.entries(params);
  if (entries.length === 0) return null;
  const rendered = entries.map(([key, value]) => `${key}=${String(value)}`).join(' ');
  return <span className="text-zinc-500">{rendered}</span>;
}
