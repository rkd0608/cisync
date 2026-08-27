import Link from 'next/link';
import type { ReactElement } from 'react';

// Uniform not-found panel for CLIENT shells. WHY a component instead of
// next/navigation notFound(): client components render after hydration and
// cannot trigger the server route's not-found boundary, so data-less lookups
// degrade to the exact same copy inline — cross-tenant ids stay
// indistinguishable from nonexistent ones (EC-050) in both paths.
export function NotFoundState(): ReactElement {
  return (
    <div className="flex flex-col items-center gap-3 py-24 text-center" role="status" data-testid="not-found-state">
      <p className="font-mono text-[11px] uppercase tracking-[0.3em] text-red-400">
        signal lost
      </p>
      <h1 className="font-mono text-lg text-zinc-200">resource not found</h1>
      <p className="max-w-md text-sm text-zinc-500">
        The control-plane returned a uniform 404 for this identifier.
      </p>
      <Link
        href="/dashboard"
        className="mt-2 rounded border border-zinc-700 px-4 py-1.5 font-mono text-xs uppercase tracking-wider text-zinc-300 hover:border-zinc-500 hover:bg-zinc-800"
      >
        back to board
      </Link>
    </div>
  );
}
