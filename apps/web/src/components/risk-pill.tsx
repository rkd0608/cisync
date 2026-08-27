import type { ReactElement } from 'react';

// §7 risk semantics via named tokens (mission Part 2 mapping):
// low=slate · medium=amber · high=orange · critical=red.
// Border-tinted outline only — never fill-flooded; red stays scarce so it
// keeps its meaning. Text mirrors the border token (outline-safe pairing).
const RISK_STYLES: Record<string, string> = {
  low: 'border-[var(--color-risk-low)]/50 text-zinc-400',
  medium: 'border-[var(--color-risk-medium)]/60 text-[var(--color-risk-medium)]',
  high: 'border-[var(--color-risk-high)]/70 text-[var(--color-risk-high)]',
  critical: 'border-[var(--color-risk-critical)]/80 text-[var(--color-risk-critical)]',
};

export function RiskPill({ risk }: { risk: string }): ReactElement {
  const style = RISK_STYLES[risk] ?? 'border-zinc-700 text-zinc-500';
  return (
    <span
      data-risk={risk}
      className={`inline-flex items-center gap-1 rounded-full border px-2 py-0.5 font-mono text-[11px] uppercase tracking-wider ${style}`}
    >
      <span className="inline-block h-1.5 w-1.5 rounded-full bg-current" aria-hidden />
      {risk}
    </span>
  );
}
