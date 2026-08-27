'use client';

import Link from 'next/link';
import type { ReactElement } from 'react';
import { PostureReview } from '@/components/posture-review';
import type { SetupState } from '@/lib/setup-machine';

// Step 3 — choose posture. Frozen ruling #7: show TRUTH (the single
// compiled-in default policy, readonly). The nothing-enforced checkbox gates
// finishing; FinishBar below routes to the live board.
export function PostureStep({
  state,
  onAckChange,
}: {
  state: SetupState;
  onAckChange: (checked: boolean) => void;
}): ReactElement {
  return (
    <section className="flex flex-col gap-4" data-testid="setup-posture-step">
      <PostureReview ackChecked={state.ackNoEnforcement} onAckChange={onAckChange} />
      {state.finished ? (
        <p data-testid="setup-finished" className="font-mono text-xs text-emerald-300">
          ✓ setup complete — nothing is enforced yet (shadow mode). heading to{' '}
          <Link href="/dashboard" className="underline">
            the board
          </Link>
          .
        </p>
      ) : null}
    </section>
  );
}

export function FinishBar({
  disabled,
  finished,
  onFinish,
}: {
  disabled: boolean;
  finished: boolean;
  onFinish: () => void;
}): ReactElement | null {
  if (finished) return null;
  return (
    <button
      type="button"
      data-testid="setup-finish"
      disabled={disabled}
      onClick={onFinish}
      className="btn-console w-fit border-emerald-500/50 text-emerald-300 enabled:hover:bg-emerald-500/10"
    >
      finish → dashboard
    </button>
  );
}
