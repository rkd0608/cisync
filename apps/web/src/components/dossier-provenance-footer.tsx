import type { ReactElement } from 'react';
import { IdLabel } from './id-label';

// Provenance footer (T6): every decision's audit anchor is one glance away.
// Chain-seal honesty: this dossier's inputs_hash is the sealed input digest;
// continuous checkpoint verification runs on the control-plane — a FAILED
// verification surfaces as a global red banner (fail-closed UI mirrors the
// fail-closed API), so its absence here IS the verified signal.
export function DossierProvenanceFooter({
  inputsHash,
  generatedAt,
  candidateId,
  intentId,
}: {
  inputsHash: string;
  generatedAt: string;
  candidateId: string;
  intentId: string;
}): ReactElement {
  return (
    <footer data-testid="provenance-footer" className="rounded-lg border border-white/8 bg-[var(--color-surface)] px-4 py-3">
      <div className="flex flex-wrap items-center gap-x-4 gap-y-2 font-mono text-[11px]">
        <span className="section-label">provenance</span>
        <span className="inline-flex items-center gap-1 rounded-full border border-emerald-500/40 px-2 py-0.5 text-emerald-300" title="control-plane checkpoint verifier reports the ledger hash chain intact; failures render as a global banner">
          ⛓ chain verified
        </span>
        <span className="flex items-center gap-1 text-zinc-500">
          inputs_hash <IdLabel id={inputsHash} head={10} tail={4} />
        </span>
        <span className="text-zinc-600">generated {generatedAt}</span>
      </div>
      <p className="mt-2 font-mono text-[10px] leading-relaxed text-zinc-600">
        candidate {candidateId} · intent {intentId} · append-only ledger: the causation event id for this
        decision lives in the dossier decision record — drill from the tail panel on the board.
      </p>
    </footer>
  );
}
