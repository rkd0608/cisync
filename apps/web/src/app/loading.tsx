export default function Loading(): React.ReactElement {
  return (
    <div aria-hidden className="flex flex-col gap-4">
      <div className="h-8 w-72 animate-pulse rounded bg-zinc-900/70" />
      <div className="h-24 animate-pulse rounded border border-zinc-900 bg-zinc-900/50" />
      <div className="h-40 animate-pulse rounded border border-zinc-900 bg-zinc-900/50" />
    </div>
  );
}
