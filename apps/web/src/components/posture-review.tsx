'use client';

import { useCallback, useEffect, useState, type ReactElement } from 'react';
import {
  tenantBudgetLimits,
  autonomySemantics,
  type ActivePolicy,
} from '@/lib/policy-schema';
import { getActivePolicies } from '@/lib/cisync-api';

type PosturePhase = 'loading' | 'ready' | 'unavailable';

// Wizard step 3 (frozen ruling #7: show TRUTH — the single compiled-in default
// policy, readonly). Endpoint absence degrades to an honest awaiting-backend
// state; the nothing-enforced checkbox still gates finishing.
export function PostureReview({
  ackChecked,
  onAckChange,
}: {
  ackChecked: boolean;
  onAckChange: (checked: boolean) => void;
}): ReactElement {
  const [phase, setPhase] = useState<PosturePhase>('loading');
  const [policy, setPolicy] = useState<ActivePolicy | null>(null);

  const load = useCallback(async (): Promise<void> => {
    setPhase('loading');
    const result = await getActivePolicies();
    const first = result.ok ? (result.data.policies[0] ?? null) : null;
    setPolicy(first);
    setPhase(first !== null ? 'ready' : 'unavailable');
  }, []);

  useEffect(() => {
    void load();
  }, [load]);

  if (phase === 'unavailable') {
    return (
      <div data-testid="posture-unavailable" className="flex flex-col gap-2">
        <div
          role="status"
          className="rounded border border-amber-500/50 bg-amber-950/20 px-4 py-3 font-mono text-xs text-amber-200"
        >
          posture unavailable — awaiting backend (GET /v1/policies/active)
        </div>
        <button
          type="button"
          onClick={() => void load()}
          className="w-fit rounded border border-zinc-700 px-3 py-1 font-mono text-xs uppercase tracking-wider text-zinc-200 hover:bg-zinc-800"
        >
          retry
        </button>
        <AckBox checked={ackChecked} onAckChange={onAckChange} />
      </div>
    );
  }

  if (phase === 'loading' || policy === null) {
    return <SkeletonBlock />;
  }

  const budget = tenantBudgetLimits(policy.body.budgets);
  return (
    <div data-testid="posture-review" className="flex flex-col gap-3">
      <p className="font-mono text-xs text-zinc-400">
        active posture · {policy.policy_id} v{policy.version}
        {policy.activated_at ? ` · activated ${policy.activated_at}` : ''}
      </p>

      <div className="rounded border border-violet-500/40 bg-violet-500/5 px-4 py-3 font-mono text-xs">
        <p className="uppercase tracking-widest text-violet-300">autonomy</p>
        <p className="mt-1 text-zinc-200">
          level {policy.body.autonomy.level} — {autonomySemantics(policy.body.autonomy)}
        </p>
      </div>

      <table className="w-full border-collapse font-mono text-xs">
        <thead>
          <tr className="border-b border-zinc-800 text-left text-[10px] uppercase tracking-widest text-zinc-400">
            <th className="py-1.5 pr-2">risk</th>
            <th className="py-1.5">required evidence</th>
          </tr>
        </thead>
        <tbody>
          {Object.entries(policy.body.required_evidence_by_risk).map(([risk, kinds]) => (
            <tr key={risk} className="border-b border-zinc-900 last:border-0">
              <td className="py-1.5 pr-2 uppercase text-zinc-300">{risk}</td>
              <td className="py-1.5 text-zinc-400">{kinds.length > 0 ? kinds.join(' · ') : '--'}</td>
            </tr>
          ))}
        </tbody>
      </table>

      <p className="font-mono text-[11px] text-zinc-500">
        budgets:{' '}
        {budget.cpuMinutes !== null
          ? `${budget.cpuMinutes} cpu-min/tenant-hour${budget.concurrentCandidates !== null ? ` · ${budget.concurrentCandidates} concurrent candidates` : ''}`
          : 'not published'}
      </p>

      <AckBox checked={ackChecked} onAckChange={onAckChange} />
    </div>
  );
}

function AckBox({
  checked,
  onAckChange,
}: {
  checked: boolean;
  onAckChange: (checked: boolean) => void;
}): ReactElement {
  return (
    <label className="flex cursor-pointer items-start gap-2 rounded border border-zinc-700 px-4 py-3 text-xs text-zinc-200">
      <input
        type="checkbox"
        data-testid="ack-no-enforcement"
        checked={checked}
        onChange={(event) => onAckChange(event.target.checked)}
        className="mt-0.5"
      />
      I understand nothing is enforced yet — every decision is recorded locally in shadow mode.
    </label>
  );
}

function SkeletonBlock(): ReactElement {
  return (
    <div aria-hidden className="h-24 animate-pulse rounded border border-zinc-900 bg-zinc-900/50" />
  );
}
