'use client';

import type { ReactElement } from 'react';
import { useAutonomyLevel } from '@/lib/use-autonomy-level';

// T5 shadow-mode surface: persistent on dashboard + every dossier while
// autonomy=0. Deliberately non-blocking — it renders alongside content, never
// instead of it, and hides itself entirely when the policies endpoint is
// unavailable (graceful-absence contract) or autonomy ≥1.
export function ShadowBanner(): ReactElement | null {
  const status = useAutonomyLevel();
  if (status !== 'shadow') return null;
  return (
    <div
      role="status"
      data-testid="shadow-banner"
      className="flex flex-wrap items-center gap-2 rounded border border-violet-500/40 bg-violet-500/10 px-4 py-2 font-mono text-xs text-violet-200"
    >
      <span className="rounded bg-violet-400/20 px-1.5 py-0.5 uppercase tracking-widest">
        shadow mode
      </span>
      <span>decisions recorded locally — not published to github · autonomy level 0 (observe/explain)</span>
    </div>
  );
}
