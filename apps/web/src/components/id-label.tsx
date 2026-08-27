'use client';

import { useState, type ReactElement } from 'react';
import { truncateMiddle } from '@/lib/format';

// §7 density rule: monospace ids truncated middle (`int_…f8`) with
// copy-on-click. WHY clipboard on click instead of a button: dense tables get
// one silent affordance, not another column of buttons; the title attribute
// keeps the full id reachable on hover for non-pointer users via keyboard
// focus + Enter (native <button> semantics).
export function IdLabel({ id, head = 6, tail = 4 }: { id: string; head?: number; tail?: number }): ReactElement {
  const [copied, setCopied] = useState(false);

  async function copyId(): Promise<void> {
    try {
      await navigator.clipboard.writeText(id);
      setCopied(true);
      window.setTimeout(() => setCopied(false), 1200);
    } catch {
      // Clipboard denied (permissions/insecure context): the title attr still
      // exposes the full id — never fabricate success.
    }
  }

  return (
    <button
      type="button"
      onClick={copyId}
      title={copied ? 'copied!' : id}
      data-testid="id-label"
      data-copied={copied}
      className="inline-flex cursor-pointer items-center font-mono text-[11px] text-zinc-500 transition-colors hover:text-cyan-300"
    >
      <span aria-hidden className="mr-1 opacity-60">
        {copied ? '✓' : '⧉'}
      </span>
      {truncateMiddle(id, head, tail)}
    </button>
  );
}
