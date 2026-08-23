import Link from 'next/link';

// Uniform not-found: a cross-tenant id is indistinguishable from a nonexistent
// one (EC-050), so the copy never hints at which it was.
export default function NotFound(): React.ReactElement {
  return (
    <div className="flex flex-col items-center gap-3 py-24 text-center">
      <p className="font-mono text-[11px] uppercase tracking-[0.3em] text-red-400">
        signal lost
      </p>
      <h1 className="font-mono text-lg text-zinc-200">resource not found</h1>
      <p className="max-w-md text-sm text-zinc-500">
        The control-plane returned a uniform 404 for this identifier.
      </p>
      <Link
        href="/"
        className="mt-2 rounded border border-zinc-700 px-4 py-1.5 font-mono text-xs uppercase tracking-wider text-zinc-300 hover:border-zinc-500 hover:bg-zinc-800"
      >
        back to board
      </Link>
    </div>
  );
}
