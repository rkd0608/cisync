import type { ReactElement } from 'react';

// Honest empty state — never fabricates rows. `hint` explains WHY the list can
// be legitimately empty (e.g. contract gaps, fresh tenant).
export function EmptyState({
  title,
  hint,
}: {
  title: string;
  hint?: string;
}): ReactElement {
  return (
    <div
      data-testid="empty-state"
      className="flex flex-col items-center gap-1 rounded border border-dashed border-zinc-800 px-5 py-8 text-center"
    >
      <p className="font-mono text-xs uppercase tracking-widest text-zinc-500">{title}</p>
      {hint ? <p className="max-w-md text-xs text-zinc-600">{hint}</p> : null}
    </div>
  );
}
