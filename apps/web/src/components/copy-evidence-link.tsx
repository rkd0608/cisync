'use client';

import { useEffect, useRef, useState, type ReactElement } from 'react';
import { evidencePermalinkPath } from '@/lib/permalink-params';

type CopyPhase = 'idle' | 'copied' | 'failed';

const PHASE_LABELS: Record<CopyPhase, string> = {
  idle: 'copy evidence link',
  copied: 'link copied',
  failed: 'clipboard unavailable — copy from the address bar',
};

// Writes the canonical pinned permalink (/candidates/{id}?at=dec_{decision_id})
// to the clipboard so it is safe to paste into PRs and Slack. Failure (e.g.
// non-secure context) degrades to an honest label, never a silent success.
export function CopyEvidenceLink({
  candidateId,
  decisionId,
}: {
  candidateId: string;
  decisionId: string;
}): ReactElement {
  const [phase, setPhase] = useState<CopyPhase>('idle');
  const resetRef = useRef<ReturnType<typeof setTimeout> | null>(null);

  useEffect(() => {
    return () => {
      if (resetRef.current !== null) clearTimeout(resetRef.current);
    };
  }, []);

  async function copy(): Promise<void> {
    try {
      const path = evidencePermalinkPath(candidateId, decisionId);
      await navigator.clipboard.writeText(`${window.location.origin}${path}`);
      setPhase('copied');
    } catch {
      setPhase('failed');
    }
    resetRef.current = setTimeout(() => setPhase('idle'), 2000);
  }

  return (
    <button
      type="button"
      data-testid="copy-evidence-link"
      data-copy-phase={phase}
      onClick={() => void copy()}
      className={`rounded border px-2 py-1 font-mono text-[11px] uppercase tracking-wider hover:bg-zinc-800 ${
        phase === 'failed'
          ? 'border-red-500/50 text-red-300'
          : phase === 'copied'
            ? 'border-emerald-500/50 text-emerald-300'
            : 'border-zinc-700 text-zinc-300'
      }`}
    >
      {PHASE_LABELS[phase]}
    </button>
  );
}
