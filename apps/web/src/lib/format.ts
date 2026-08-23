// Pure display-math helpers. No React, no fetch — fully unit-testable.

// D8 completeness arrives as 0..1; clamp defensively so a bad upstream number
// can never distort the bar (NaN → 0).
export function completenessPercent(value: number): number {
  if (!Number.isFinite(value)) return 0;
  const clamped = Math.min(1, Math.max(0, value));
  return Math.round(clamped * 100);
}

export type CountdownTone = 'none' | 'calm' | 'soon' | 'overdue';

export interface Countdown {
  label: string;
  tone: CountdownTone;
}

const HOUR_MS = 3_600_000;
const DAY_MS = 24 * HOUR_MS;
const SOON_WINDOW_MS = 6 * HOUR_MS;

export function formatCountdown(
  deadlineIso: string | null | undefined,
  nowMs: number,
): Countdown {
  if (!deadlineIso) return { label: 'no deadline', tone: 'none' };
  const deadlineMs = Date.parse(deadlineIso);
  if (Number.isNaN(deadlineMs)) return { label: 'invalid deadline', tone: 'none' };

  const diff = deadlineMs - nowMs;
  if (diff <= 0) return { label: `overdue ${formatSpan(-diff)} ago`, tone: 'overdue' };
  if (diff <= SOON_WINDOW_MS) return { label: `due in ${formatSpan(diff)}`, tone: 'soon' };
  return { label: `due in ${formatSpan(diff)}`, tone: 'calm' };
}

function formatSpan(spanMs: number): string {
  const days = Math.floor(spanMs / DAY_MS);
  const hours = Math.floor((spanMs % DAY_MS) / HOUR_MS);
  const minutes = Math.floor((spanMs % HOUR_MS) / 60_000);
  if (days > 0) return `${days}d ${hours}h`;
  if (hours > 0) return `${hours}h ${minutes}m`;
  if (minutes >= 1) return `${minutes}m`;
  return '<1m';
}

export function shortSha(sha: string): string {
  return sha.length > 10 ? `${sha.slice(0, 8)}…` : sha;
}
