import Link from 'next/link';
import type { ReactElement } from 'react';
import { truncateMiddle } from '@/lib/format';

export interface FailedEvidenceItem {
  evId: string;
  reason?: string;
}

const AMBER_BOX =
  'flex flex-col gap-1 rounded border border-amber-500/50 bg-amber-950/20 px-4 py-3 font-mono text-xs text-amber-200';

// ⏹ superseded state (§2.5): route readers to the live representative — via
// the cluster (representative first) when one exists, else the parent intent.
export function SupersededBanner({
  clusterId,
  intentId,
}: {
  clusterId: string | null;
  intentId: string;
}): ReactElement {
  return (
    <div role="status" data-testid="superseded-banner" className={AMBER_BOX}>
      <span className="uppercase tracking-widest">⏹ superseded candidate</span>
      <p>
        This candidate was superseded — see the{' '}
        {clusterId ? (
          <Link href={`/clusters/${clusterId}`} className="underline hover:text-amber-100">
            cluster representative
          </Link>
        ) : (
          <Link href={`/intents/${intentId}`} className="underline hover:text-amber-100">
            parent intent
          </Link>
        )}{' '}
        for the current decision.
      </p>
    </div>
  );
}

// Invalidated-evidence notice (§2.5, amber): lists ev_ids + reason. Rendered
// only when accepted evidence actually carries failed verdicts — never as a
// decorative warning.
export function InvalidatedEvidenceNotice({
  items,
}: {
  items: FailedEvidenceItem[];
}): ReactElement | null {
  if (items.length === 0) return null;
  return (
    <div role="alert" data-testid="invalidated-evidence" className={AMBER_BOX}>
      <span className="uppercase tracking-widest">✕ evidence invalidated ({items.length})</span>
      <ul className="flex flex-col gap-0.5">
        {items.map((item) => (
          <li key={item.evId}>
            {item.evId}
            {item.reason ? ` — ${item.reason}` : ''}
          </li>
        ))}
      </ul>
    </div>
  );
}

// ?at pin states (frozen ruling #2: live re-render + honest mismatch banner).
export function PinUnavailableNotice(): ReactElement {
  return (
    <div role="status" data-testid="pin-unavailable" className={AMBER_BOX}>
      decision snapshot unavailable — showing latest
    </div>
  );
}

export function PinnedDecisionChip({ decisionId }: { decisionId: string }): ReactElement {
  return (
    <span
      data-testid="pin-active"
      className="rounded border border-cyan-500/40 bg-cyan-500/10 px-1.5 py-0.5 font-mono text-[10px] uppercase tracking-wider text-cyan-300"
    >
      pinned · {truncateMiddle(decisionId)}
    </span>
  );
}
