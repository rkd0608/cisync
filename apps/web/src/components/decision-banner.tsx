import type { ReactElement } from 'react';
import type { Decision, DecisionFactor } from '@/lib/api-schemas';
import { calibratedConfidence, verbPhrase } from '@/lib/calibrated-copy';

// §7: verdict kinds render outline-only — color encodes state, text encodes
// meaning (colorblind-safe by construction); no fill-flood. Mission verdict
// mapping: eligible=emerald · rejected=rose · deferred=amber.
const VERB_STYLES: Record<string, string> = {
  eligible_for_merge_train: 'border-emerald-500 text-emerald-200',
  rejected: 'border-rose-500 text-rose-200',
  deferred: 'border-amber-500 text-amber-200',
};

// T1: factors render straight from the ledger record — name=value plus source,
// never a UI-invented rationale.
function FactorList({ factors }: { factors: DecisionFactor[] }): ReactElement {
  return (
    <ul className="mt-2 flex flex-col gap-1 font-mono text-[11px] text-zinc-400">
      {factors.map((factor, index) => (
        <li key={`${factor.name}-${index}`} className="flex flex-wrap items-baseline gap-2">
          <span className="text-zinc-100">{factor.name}</span>
          <span className="text-zinc-300">={String(factor.value)}</span>
          <span className="text-zinc-400">{factor.source}</span>
        </li>
      ))}
    </ul>
  );
}

// L0→L1 trust surface (§7 depth rules): verdict + calibrated confidence +
// summary first; explanation.factors expand on demand. The expander disappears
// entirely when the decision carries no factors (no dead controls).
export function DecisionBanner({ decision }: { decision: Decision }): ReactElement {
  const calibrated = calibratedConfidence(decision.confidence);
  const policyLabel =
    decision.policy?.policy_id !== undefined
      ? `${decision.policy.policy_id} v${decision.policy.version ?? '?'}`
      : 'unresolved policy';
  const factors = decision.explanation?.factors ?? [];
  return (
    <section
      data-testid="decision-banner"
      data-verb={decision.verb}
      className={`rounded-lg border px-5 py-4 font-mono backdrop-blur-md bg-[var(--color-surface-raised)]/85 ${VERB_STYLES[decision.verb] ?? 'border-zinc-700'}`}
    >
      <p className="text-[11px] uppercase tracking-widest opacity-70">
        decision {decision.decision_id}
      </p>
      <p className="mt-1 text-lg">{verbPhrase(decision.verb)}</p>
      <p className="mt-1 text-xs opacity-80">
        confidence {calibrated.label} · policy {policyLabel}
      </p>
      <p className="mt-2 max-w-3xl font-sans text-sm text-zinc-300">{decision.summary}</p>
      {factors.length > 0 ? (
        <details className="mt-3 border-t border-white/10 pt-2">
          <summary className="cursor-pointer select-none text-[11px] uppercase tracking-widest opacity-70 hover:opacity-100">
            why this decision · {factors.length} factors
          </summary>
          <FactorList factors={factors} />
        </details>
      ) : null}
    </section>
  );
}
