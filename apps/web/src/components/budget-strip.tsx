'use client';

import { useEffect, useState, type ReactElement } from 'react';
import { request } from '@/lib/cisync-api';
import { budgetsResponseSchema, deriveBudgetMeters, type BudgetMeter } from '@/lib/budget-schema';

// Graceful-absence contract (§2.3): the board never waits on budgets. A
// failed/absent endpoint resolves null and the strip renders nothing at all —
// no placeholder box, no fake zero.
export function useBudgetMeters(): BudgetMeter[] | null {
  const [meters, setMeters] = useState<BudgetMeter[] | null>(null);
  useEffect(() => {
    let live = true;
    void request(budgetsResponseSchema, '/v1/budgets').then((result) => {
      if (!live) return;
      setMeters(result.ok ? deriveBudgetMeters(result.data) : null);
    });
    return () => {
      live = false;
    };
  }, []);
  return meters;
}

export function BudgetStrip({ meters }: { meters: BudgetMeter[] }): ReactElement | null {
  if (meters.length === 0) return null;
  return (
    <div
      data-testid="budget-strip"
      className="flex flex-wrap items-center gap-x-5 gap-y-1 rounded-lg border border-white/8 bg-[var(--color-surface)] px-4 py-2"
    >
      <span className="section-label">budget</span>
      {meters.map((meter) => (
        <span key={meter.label} className="flex items-center gap-2 font-mono text-[11px] text-zinc-400">
          <MeterBar meter={meter} />
          <span title={meter.limit === null ? 'consumed · no published limit' : `consumed of ${meter.limit}`}>
            {meter.consumed ?? '—'}
            {meter.limit !== null ? `/${meter.limit}` : ''}
          </span>
        </span>
      ))}
    </div>
  );
}

function MeterBar({ meter }: { meter: BudgetMeter }): ReactElement {
  // Bar renders only when BOTH numbers exist; a consumed value without a
  // limit can still show honestly as text.
  const ratio =
    meter.consumed !== null && meter.limit !== null && meter.limit > 0
      ? Math.min(1, meter.consumed / meter.limit)
      : null;
  const tone =
    ratio === null
      ? 'bg-zinc-700'
      : ratio >= 0.9
        ? 'bg-[var(--color-risk-critical)]'
        : ratio >= 0.7
          ? 'bg-[var(--color-risk-high)]'
          : 'bg-emerald-400';
  return (
    <span aria-hidden className="inline-block h-1.5 w-16 overflow-hidden rounded-full bg-zinc-800">
      {ratio !== null ? <span data-testid="budget-fill" className={`block h-full ${tone}`} style={{ width: `${Math.round(ratio * 100)}%` }} /> : null}
    </span>
  );
}
