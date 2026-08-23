import type { ReactElement } from 'react';
import type { EvidenceDossier } from '@/lib/api-schemas';

const VERB_STYLES: Record<string, string> = {
  eligible_for_merge_train: 'border-emerald-500 bg-emerald-500/10 text-emerald-200',
  rejected: 'border-red-500 bg-red-500/10 text-red-200',
  deferred: 'border-amber-500 bg-amber-500/10 text-amber-200',
};

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

// Full §7 dossier render. Deferred evidence / known uncertainty /
// required_post_merge are mandatory sections: they always render, even empty,
// so silence is never mistaken for absence of risk.
export function DossierView({ dossier }: { dossier: EvidenceDossier }): ReactElement {
  const { decision } = dossier;
  const policyLabel =
    decision.policy?.policy_id !== undefined
      ? `${decision.policy.policy_id} v${decision.policy?.version ?? '?'}`
      : 'unresolved policy';
  return (
    <article className="flex flex-col gap-4" data-testid="dossier-view">
      <section
        className={`rounded border px-5 py-4 font-mono ${VERB_STYLES[decision.verb] ?? 'border-zinc-700'}`}
      >
        <p className="text-[11px] uppercase tracking-widest opacity-70">decision {decision.decision_id}</p>
        <p className="mt-1 text-lg">{decision.verb}</p>
        <p className="mt-1 text-xs opacity-80">
          confidence {(decision.confidence * 100).toFixed(1)}% · policy {policyLabel}
        </p>
        <p className="mt-2 max-w-3xl font-sans text-sm text-zinc-300">{decision.summary}</p>
        <p className="mt-2 text-[11px] text-zinc-500">
          inputs_hash {dossier.inputs_hash} · generated {dossier.generated_at}
        </p>
      </section>

      <section className="rounded border border-zinc-800 px-4 py-4">
        <h3 className="font-mono text-[11px] uppercase tracking-widest text-emerald-400">
          accepted evidence ({dossier.evidence_accepted.length})
        </h3>
        {dossier.evidence_accepted.length === 0 ? (
          <p className="mt-2 text-xs text-zinc-600">none recorded</p>
        ) : (
          <ul className="mt-2 flex flex-col gap-1 font-mono text-xs">
            {dossier.evidence_accepted.map((ev) => (
              <li key={ev.ev_id} className="flex flex-wrap items-baseline gap-2">
                <span
                  className={
                    ev.verdict === 'pass'
                      ? 'text-emerald-300'
                      : 'text-red-300'
                  }
                >
                  [{ev.verdict}]
                </span>
                <span className="text-zinc-100">{ev.kind}</span>
                <span className="text-zinc-600">{ev.ev_id}</span>
                <MetaList meta={ev.meta ?? {}} />
              </li>
            ))}
          </ul>
        )}
      </section>

      <section className="rounded border border-amber-900/50 bg-amber-950/20 px-4 py-4">
        <h3 className="font-mono text-[11px] uppercase tracking-widest text-amber-400">
          deferred evidence — with reasons ({dossier.evidence_deferred.length})
        </h3>
        {dossier.evidence_deferred.length === 0 ? (
          <p className="mt-2 text-xs text-zinc-600">nothing deferred</p>
        ) : (
          <ul className="mt-2 flex flex-col gap-2 text-xs">
            {dossier.evidence_deferred.map((item) => (
              <li key={item.kind}>
                <span className="font-mono text-amber-200">{item.kind}</span>
                <span className="ml-2 text-zinc-300">{item.reason}</span>
                <span className="ml-2 font-mono text-zinc-500">stage: {item.stage_required}</span>
              </li>
            ))}
          </ul>
        )}
      </section>

      <div className="grid gap-4 md:grid-cols-2">
        <section className="rounded border border-zinc-800 px-4 py-4">
          <h3 className="font-mono text-[11px] uppercase tracking-widest text-sky-400">
            known uncertainty ({dossier.known_uncertainty.length})
          </h3>
          <UncertaintyList items={dossier.known_uncertainty} />
        </section>
        <section className="rounded border border-zinc-800 px-4 py-4">
          <h3 className="font-mono text-[11px] uppercase tracking-widest text-violet-400">
            required post-merge ({dossier.required_post_merge.length})
          </h3>
          <PostMergeList items={dossier.required_post_merge} />
        </section>
      </div>
    </article>
  );
}

type UncertaintyItem = EvidenceDossier['known_uncertainty'][number];
type PostMergeEntry = EvidenceDossier['required_post_merge'][number];

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
