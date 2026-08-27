import type { ReactElement } from 'react';

// Human-in-the-loop slot at H-gated states (PRODUCT_UX_PLAN §5: unblock is
// ALWAYS a human variant). The G2 action endpoints (POST /v1/human-decisions/
// {id}:approve|reject|unblock) are pending backend CORE-v0.2, so the slot
// renders as an honest queue reference with reason-required copy — buttons
// that cannot act would violate the no-dead-controls rule.
export function HumanActionSlot({ intentId }: { intentId: string }): ReactElement {
  return (
    <div
      role="status"
      data-testid="human-action-slot"
      className="flex flex-col gap-2 rounded-lg border border-orange-500/40 bg-orange-500/5 px-4 py-3"
    >
      <p className="font-mono text-[11px] uppercase tracking-widest text-orange-300">
        ⏸ blocked — awaiting human decision
      </p>
      <p className="text-xs leading-relaxed text-zinc-300">
        This intent sits at a human-gated transition. A ruling on{' '}
        <span className="font-mono text-zinc-400">{intentId}</span> must arrive through the
        human-decisions flow (reason required on both approve and reject — symmetric friction).
        The API surface ships with the decisions queue; operators act via the control-plane CLI today.
      </p>
    </div>
  );
}
