import type { ReactElement } from 'react';

const RELATION_GLYPHS: Record<string, string> = {
  duplicate_of: '= dup',
  alternative_of: '<> alt',
  composable_with: '+ comp',
  conflicts_with: 'x conf',
  prerequisite_of: '^ prereq',
  overlapping: 'n overlap',
};

export function RelationBadge({ relation }: { relation: string | null }): ReactElement {
  if (!relation) {
    return <span className="font-mono text-[11px] text-zinc-600">--</span>;
  }
  const label = RELATION_GLYPHS[relation] ?? relation;
  return (
    <span className="inline-flex items-center rounded border border-zinc-700 bg-zinc-800/60 px-1.5 py-0.5 font-mono text-[11px] text-zinc-300">
      {label}
    </span>
  );
}
