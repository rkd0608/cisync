import { describe, expect, it } from 'vitest';
import { completenessPercent, formatCountdown, shortSha } from './format';

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
