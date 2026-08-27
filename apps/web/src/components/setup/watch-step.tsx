'use client';

import Link from 'next/link';
import type { ReactElement } from 'react';
import { calibratedConfidence, verbPhrase } from '@/lib/calibrated-copy';
import { truncateMiddle } from '@/lib/format';
import type { WatchStatus } from '@/lib/setup-machine';

export interface WatchSignal {
  candidateId: string | null;
  verb: string | null;
  confidence: number | null;
}

// Step 2 — first verification watch (mission Part 1): show the most recent
// candidate/decision straight from the ledger tail once any delivery arrives.
// Copy is deliberately teaching: an empty ledger is EXPECTED on a fresh
// tenant, so the quiet state explains how to generate traffic instead of
// implying failure.
export function WatchStep({
  watch,
  signal,
  onContinue,
}: {
  watch: WatchStatus;
  signal: WatchSignal;
  onContinue: () => void;
}): ReactElement {
  const observed = watch === 'candidate_seen' || watch === 'decision_seen';
  return (
    <section className="flex flex-col gap-4" data-testid="setup-watch-step" data-state={watch}>
      <div className="rounded-lg border border-white/8 bg-[var(--color-surface)] px-4 py-3">
        <p className="section-label">what this step proves</p>
        <p className="mt-1 text-xs leading-relaxed text-zinc-400">
          Every governed change produces a candidate in the ledger; every decision renders a calibrated verdict.
          Watching one arrive end-to-end is the fastest proof the pipe works — before you rely on it.
        </p>
      </div>

      {watch === 'listening' ? (
        <p className="flex items-center gap-2 font-mono text-xs text-zinc-400">
          <span aria-hidden className="inline-block h-2 w-2 animate-pulse rounded-full bg-[var(--color-signal)]" />
          listening for the first delivery… open a PR (or POST /v1/change-intents) to generate traffic.
        </p>
      ) : null}

      {watch === 'awaiting_backend' ? (
        <p role="status" className="rounded-lg border border-amber-500/50 bg-amber-950/20 px-4 py-3 font-mono text-xs text-amber-200">
          ledger tail unreachable right now — events are not lost, polling resumes automatically. nothing can be confirmed until it answers.
        </p>
      ) : null}

      {observed && signal.candidateId !== null ? (
        <div data-testid="watch-signal" className="flex flex-col gap-2 rounded-lg border border-emerald-500/40 bg-emerald-500/5 px-4 py-3">
          <p className="font-mono text-xs text-emerald-300">
            ✓ delivery received · candidate{' '}
            <Link href={`/candidates/${signal.candidateId}`} className="underline hover:text-emerald-200">
              {truncateMiddle(signal.candidateId)}
            </Link>
          </p>
          {signal.verb !== null ? (
            <p className="font-mono text-xs text-zinc-300" data-testid="watch-decision">
              ⟳ → ✓ decision rendered: {verbPhrase(signal.verb)}
              {signal.confidence !== null ? ` · ${calibratedConfidence(signal.confidence).label}` : ''}
            </p>
          ) : (
            <p className="font-mono text-xs text-zinc-500">○ validation running — the decision will land here when evidence resolves.</p>
          )}
        </div>
      ) : null}

      {observed ? (
        <button type="button" onClick={onContinue} className="btn-console w-fit border-emerald-500/50 text-emerald-300">
          signal received — review posture →
        </button>
      ) : null}
    </section>
  );
}
