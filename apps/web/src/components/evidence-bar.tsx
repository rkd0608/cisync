import type { ReactElement } from 'react';
import { completenessPercent } from '@/lib/format';

// Renders the D8 evidence_completeness_pct (0..1) as a console-style bar.
// Math is clamped in completenessPercent so malformed input can never distort
// the visual — it degrades to 0%, it does not crash.
export function EvidenceBar({
  pct,
  label = 'evidence',
}: {
  pct: number;
  label?: string;
}): ReactElement {
  const percent = completenessPercent(pct);
  const tone =
    percent >= 100
      ? 'bg-emerald-400'
      : percent >= 60
        ? 'bg-cyan-400'
        : percent > 0
          ? 'bg-amber-400'
          : 'bg-zinc-600';
  return (
    <div className="flex min-w-[140px] items-center gap-2" title={`${label} completeness`}>
      <div className="h-1.5 w-full overflow-hidden rounded-full bg-zinc-800">
        <div
          data-testid="evidence-fill"
          className={`h-full rounded-full ${tone}`}
          style={{ width: `${percent}%` }}
        />
      </div>
      <span className="w-9 shrink-0 text-right font-mono text-[11px] tabular-nums text-zinc-400">
        {percent}%
      </span>
    </div>
  );
}
