import type { ReactElement } from 'react';

export interface ApiErrorView {
  code: string;
  message: string;
}

// Honest failure surface: shows the machine-readable code from the contract's
// ErrorEnvelope (or schema_mismatch when the body violated the v1 shapes) and
// an optional retry action supplied by client callers.
export function ErrorState({
  error,
  onRetry,
}: {
  error: ApiErrorView;
  onRetry?: () => void;
}): ReactElement {
  return (
    <div
      role="alert"
      data-testid="error-state"
      className="flex flex-col gap-2 rounded border border-red-900/60 bg-red-950/30 px-5 py-6"
    >
      <div className="flex items-center gap-2 font-mono text-xs">
        <span className="rounded bg-red-500/20 px-1.5 py-0.5 uppercase tracking-wider text-red-300">
          {error.code}
        </span>
        <span className="text-zinc-400">control-plane response rejected</span>
      </div>
      <p className="text-sm text-zinc-300">{error.message}</p>
      {onRetry ? (
        <button
          type="button"
          onClick={onRetry}
          className="mt-1 w-fit rounded border border-zinc-700 px-3 py-1 font-mono text-xs uppercase tracking-wider text-zinc-200 hover:border-zinc-500 hover:bg-zinc-800"
        >
          retry
        </button>
      ) : null}
    </div>
  );
}
