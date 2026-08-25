import Link from 'next/link';
import type { ReactElement } from 'react';

export interface EmptyStateAction {
  label: string;
  href: string;
}

// Teaching empty-state contract (PRODUCT_UX_PLAN §2.8): every empty state
// answers three lines — what this shows / why it's empty / one action to
// change that. Absence of data is content, never blank space; `action` is
// omitted only when the empty state itself IS the good outcome.
export function EmptyState({
  what,
  whyEmpty,
  action,
}: {
  what: string;
  whyEmpty?: string;
  action?: EmptyStateAction;
}): ReactElement {
  return (
    <div
      data-testid="empty-state"
      className="flex flex-col items-center gap-1 rounded border border-dashed border-zinc-800 px-5 py-8 text-center"
    >
      <p data-testid="empty-what" className="font-mono text-xs uppercase tracking-widest text-zinc-500">
        {what}
      </p>
      {whyEmpty ? (
        <p data-testid="empty-why" className="max-w-md text-xs text-zinc-600">
          {whyEmpty}
        </p>
      ) : null}
      {action ? (
        <Link
          href={action.href}
          data-testid="empty-action"
          className="mt-1 font-mono text-xs text-cyan-400 hover:text-cyan-300 hover:underline"
        >
          {action.label} →
        </Link>
      ) : null}
    </div>
  );
}
