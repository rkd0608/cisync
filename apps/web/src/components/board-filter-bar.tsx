'use client';

import type { ReactElement } from 'react';
import {
  GROUP_MODES,
  type BoardFilters,
  type BoardGroupMode,
} from '@/lib/board-filters';

export interface FilterBarOptions {
  repos: string[];
  risks: string[];
  origins: string[];
}

function Select({
  label,
  value,
  options,
  onChange,
}: {
  label: string;
  value: string | null;
  options: string[];
  onChange: (value: string | null) => void;
}): ReactElement {
  return (
    <label className="flex items-center gap-1 font-mono text-[11px] uppercase tracking-widest text-zinc-600">
      {label}
      <select
        data-testid={`filter-${label}`}
        value={value ?? ''}
        onChange={(event) => onChange(event.target.value === '' ? null : event.target.value)}
        className="rounded border border-zinc-700 bg-zinc-950 px-1.5 py-1 font-mono text-[11px] normal-case tracking-normal text-zinc-200"
      >
        <option value="">all</option>
        {options.map((option) => (
          <option key={option} value={option}>
            {option}
          </option>
        ))}
      </select>
    </label>
  );
}

// §2.3 filter bar: repo / risk / origin selects populated from live board data
// plus the state|risk group-by toggle. Changes are pushed to the URL by the
// parent so filters survive reloads and are shareable.
export function BoardFilterBar({
  filters,
  groupBy,
  options,
  onChange,
  onGroupChange,
}: {
  filters: BoardFilters;
  groupBy: BoardGroupMode;
  options: FilterBarOptions;
  onChange: (filters: BoardFilters) => void;
  onGroupChange: (mode: BoardGroupMode) => void;
}): ReactElement {
  return (
    <div
      data-testid="board-filter-bar"
      className="flex flex-wrap items-center gap-x-4 gap-y-2 rounded border border-zinc-800 bg-zinc-950 px-4 py-2"
    >
      <span className="font-mono text-[11px] uppercase tracking-widest text-zinc-500">filters</span>
      <Select
        label="repo"
        value={filters.repo}
        options={options.repos}
        onChange={(repo) => onChange({ ...filters, repo })}
      />
      <Select
        label="risk"
        value={filters.risk}
        options={options.risks}
        onChange={(risk) => onChange({ ...filters, risk })}
      />
      <Select
        label="origin"
        value={filters.origin}
        options={options.origins}
        onChange={(origin) => onChange({ ...filters, origin })}
      />
      <div className="flex items-center gap-1 font-mono text-[11px] uppercase tracking-widest text-zinc-600">
        group
        <div className="flex overflow-hidden rounded border border-zinc-700">
          {GROUP_MODES.map((mode) => (
            <button
              key={mode}
              type="button"
              data-group-mode={mode}
              data-active={groupBy === mode || undefined}
              onClick={() => onGroupChange(mode)}
              className={`px-2 py-1 font-mono text-[11px] ${
                groupBy === mode ? 'bg-zinc-800 text-cyan-300' : 'text-zinc-400 hover:bg-zinc-900'
              }`}
            >
              {mode}
            </button>
          ))}
        </div>
      </div>
      {filters.repo !== null || filters.risk !== null || filters.origin !== null ? (
        <button
          type="button"
          onClick={() => onChange({ repo: null, risk: null, origin: null })}
          className="ml-auto font-mono text-[11px] uppercase tracking-widest text-cyan-400 hover:text-cyan-300"
        >
          clear
        </button>
      ) : null}
    </div>
  );
}
