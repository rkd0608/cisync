import { describe, expect, it } from 'vitest';
import { completenessPercent, formatCountdown, relativeAge, shortSha, truncateMiddle } from './format';

describe('completenessPercent (D8 bar math)', () => {
  it('maps 0..1 onto 0..100 with rounding', () => {
    expect(completenessPercent(0)).toBe(0);
    expect(completenessPercent(0.333)).toBe(33);
    expect(completenessPercent(0.999)).toBe(100);
    expect(completenessPercent(1)).toBe(100);
  });

  it('clamps out-of-range values instead of distorting the bar', () => {
    expect(completenessPercent(-0.5)).toBe(0);
    expect(completenessPercent(1.5)).toBe(100);
  });

  it('degrades non-finite input to 0 (fail-closed: schema bounds pct to 0..1)', () => {
    expect(completenessPercent(Number.NaN)).toBe(0);
    expect(completenessPercent(Number.POSITIVE_INFINITY)).toBe(0);
    expect(completenessPercent(Number.NEGATIVE_INFINITY)).toBe(0);
  });
});

describe('formatCountdown', () => {
  const now = Date.parse('2026-08-23T12:00:00Z');

  it('reports missing and unparseable deadlines without crashing', () => {
    expect(formatCountdown(null, now).label).toBe('no deadline');
    expect(formatCountdown(undefined, now).tone).toBe('none');
    expect(formatCountdown('not-a-date', now).tone).toBe('none');
  });

  it('formats future spans at day/hour/minute granularity', () => {
    expect(formatCountdown('2026-08-25T12:00:00Z', now)).toEqual({
      label: 'due in 2d 0h',
      tone: 'calm',
    });
    expect(formatCountdown('2026-08-23T15:30:00Z', now)).toEqual({
      label: 'due in 3h 30m',
      tone: 'soon',
    });
    expect(formatCountdown('2026-08-23T12:40:00Z', now)).toEqual({
      label: 'due in 40m',
      tone: 'soon',
    });
  });

  it('flags overdue deadlines with elapsed span', () => {
    expect(formatCountdown('2026-08-23T10:00:00Z', now)).toEqual({
      label: 'overdue 2h 0m ago',
      tone: 'overdue',
    });
  });
});

describe('shortSha', () => {
  it('truncates long shas and keeps short ones intact', () => {
    expect(shortSha('a'.repeat(40))).toBe(`${'a'.repeat(8)}…`);
    expect(shortSha('abc')).toBe('abc');
  });
});

describe('truncateMiddle', () => {
  it('keeps head and tail of long ids with an ellipsis between', () => {
    const id = 'dec_01J8ZC7XK9Q2W3E4R5T6Y70003';
    expect(truncateMiddle(id)).toBe('dec_01…0003');
  });

  it('returns short values untouched (no ellipsis for its own sake)', () => {
    expect(truncateMiddle('dec_1234')).toBe('dec_1234');
  });

  it('honors custom window sizes', () => {
    expect(truncateMiddle('abcdefghij', 2, 2)).toBe('ab…ij');
  });
});

describe('relativeAge (installation delivery ages)', () => {
  const now = Date.parse('2026-08-23T12:00:00Z');

  it('formats seconds, minutes, then hours', () => {
    expect(relativeAge('2026-08-23T11:59:48Z', now)).toBe('12s ago');
    expect(relativeAge('2026-08-23T11:26:00Z', now)).toBe('34m ago');
    expect(relativeAge('2026-08-23T09:00:00Z', now)).toBe('3h ago');
  });

  it('degrades missing and unparseable timestamps honestly', () => {
    expect(relativeAge(null, now)).toBe('--');
    expect(relativeAge(undefined, now)).toBe('--');
    expect(relativeAge('not-a-date', now)).toBe('unknown');
  });

  it('clamps future timestamps to zero instead of going negative', () => {
    expect(relativeAge('2026-08-23T12:00:10Z', now)).toBe('0s ago');
  });
});
