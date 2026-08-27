import type { ReactElement } from 'react';

// Shared loading skeleton (§2.3 states). One pulse recipe so every async
// surface degrades visually the same way.
export function Skeleton({
  className = '',
  rounded = 'rounded-lg',
}: {
  className?: string;
  rounded?: string;
}): ReactElement {
  return (
    <div
      aria-hidden
      data-testid="skeleton"
      className={`${rounded} border border-white/5 bg-white/[0.03] animate-pulse ${className}`}
    />
  );
}

export function SkeletonRows({ rows, rowClassName = 'h-14' }: { rows: number; rowClassName?: string }): ReactElement {
  return (
    <div aria-hidden className="flex flex-col gap-2" data-testid="skeleton-rows">
      {Array.from({ length: rows }, (_, index) => (
        <Skeleton key={index} className={rowClassName} />
      ))}
    </div>
  );
}
