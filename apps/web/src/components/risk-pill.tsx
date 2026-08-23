import type { ReactElement } from 'react';

const RISK_STYLES: Record<string, string> = {
  low: 'border-emerald-500/40 text-emerald-300',
  medium: 'border-amber-500/40 text-amber-300',
  high: 'border-orange-500/50 text-orange-300',
  critical: 'border-red-500/60 bg-red-500/10 text-red-300',
};

export function RiskPill({ risk }: { risk: string }): ReactElement {
  const style = RISK_STYLES[risk] ?? 'border-zinc-600 text-zinc-400';
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
