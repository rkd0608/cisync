import type { ReactElement } from 'react';
import type { ConflictRef } from '@/lib/api-schemas';

const RECOMMENDATION_STYLES: Record<string, string> = {
  coordinate: 'text-amber-300 border-amber-700/60',
  proceed: 'text-emerald-300 border-emerald-700/60',
  wait: 'text-sky-300 border-sky-700/60',
};

// Renders IntentGrant.conflicts[] (openapi ConflictRef shape). Typed against
// the contract schema; empty input renders the honest-empty panel, never a
// fabricated row.
export function ConflictPanel({ conflicts }: { conflicts: ConflictRef[] }): ReactElement {
  if (conflicts.length === 0) {
    return (
      <section className="rounded border border-dashed border-zinc-800 px-4 py-4">
        <h3 className="font-mono text-[11px] uppercase tracking-widest text-zinc-500">
          surface conflicts
        </h3>
        <p className="mt-1 text-xs text-zinc-400">
          No conflicts on record. Note: the v1 GET /change-intents/&#123;id&#125; response
          (Intent) does not carry the conflicts[] field — it is only returned by
          POST /change-intents (IntentGrant). This panel renders live conflict
          data only when the contract exposes it.
        </p>
      </section>
    );
  }
  return (
    <section className="rounded border border-amber-900/50 bg-amber-950/20 px-4 py-4">
      <h3 className="font-mono text-[11px] uppercase tracking-widest text-amber-400">
        surface conflicts ({conflicts.length})
      </h3>
      <ul className="mt-2 flex flex-col gap-2">
        {conflicts.map((conflict) => (
          <li
            key={`${conflict.intent_id}-${conflict.owner}`}
            className="flex flex-wrap items-center gap-2 font-mono text-xs"
          >
            <span className="rounded border border-zinc-700 px-1.5 py-0.5 text-zinc-300">
              {conflict.relation}
            </span>
            <span className="text-zinc-200">{conflict.intent_id}</span>
            <span className="text-zinc-500">owner: {conflict.owner}</span>
            <span
              className={`ml-auto rounded border px-1.5 py-0.5 uppercase tracking-wider ${
                RECOMMENDATION_STYLES[conflict.recommendation] ?? 'border-zinc-700 text-zinc-300'
              }`}
            >
              {conflict.recommendation}
            </span>
          </li>
        ))}
      </ul>
    </section>
  );
}
