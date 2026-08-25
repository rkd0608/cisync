import type { ReactElement } from 'react';

const CHIP_LABELS = {
  github_check: 'via github check',
} as const;

export type ContextKind = keyof typeof CHIP_LABELS;

// Arrival-context chips (plan §3.2): mark how a reader reached this page so
// support/debug can trace link provenance. More arrival contexts land with
// later waves; unknown kinds never render.
export function ContextChip({ kind }: { kind: ContextKind }): ReactElement {
  return (
    <span
      data-context={kind}
      className="rounded border border-violet-500/40 bg-violet-500/10 px-1.5 py-0.5 font-mono text-[10px] uppercase tracking-wider text-violet-300"
    >
      {CHIP_LABELS[kind]}
    </span>
  );
}
