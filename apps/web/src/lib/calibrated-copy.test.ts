import { describe, expect, it } from 'vitest';
import {
  BANNED_PHRASES,
  CONFIDENCE_THRESHOLDS,
  calibratedConfidence,
  confidenceWord,
  findBannedPhrases,
  verbPhrase,
} from './calibrated-copy';

describe('confidenceWord (T4 mapping)', () => {
  it.each([
    [1, 'high'],
    [0.95, 'high'],
    [0.9501, 'high'],
    [0.9499, 'moderate'],
    [0.94, 'moderate'],
    [0.8, 'moderate'],
    [0.79, 'low'],
    [0.5, 'low'],
    [0.49, 'insufficient'],
    [0, 'insufficient'],
  ] as const)('maps %s → %s', (input, expected) => {
    expect(confidenceWord(input)).toBe(expected);
  });

  it('treats out-of-domain numbers as insufficient, never as high', () => {
    expect(confidenceWord(1.01)).toBe('insufficient');
    expect(confidenceWord(-0.01)).toBe('insufficient');
    expect(confidenceWord(Number.NaN)).toBe('insufficient');
    expect(confidenceWord(Number.POSITIVE_INFINITY)).toBe('insufficient');
  });

  it('keeps the frozen threshold constants in sync with plan §4', () => {
    expect(CONFIDENCE_THRESHOLDS).toEqual({ high: 0.95, moderate: 0.8, low: 0.5 });
  });
});

describe('calibratedConfidence (words + numbers together)', () => {
  it('renders word and number side by side', () => {
    const calibrated = calibratedConfidence(0.94);
    expect(calibrated.word).toBe('moderate');
    expect(calibrated.pct).toBe('94.0%');
    expect(calibrated.label).toBe('moderate · 94.0% confidence');
  });

  it('degrades unusable numbers honestly instead of inventing one', () => {
    const calibrated = calibratedConfidence(Number.NaN);
    expect(calibrated.word).toBe('insufficient');
    expect(calibrated.pct).toBe('--');
    expect(calibrated.label).toBe('insufficient · -- confidence');
  });
});

describe('verbPhrase (ledger verbs verbatim)', () => {
  it('maps every ledger verb without paraphrasing toward approval language', () => {
    expect(verbPhrase('eligible_for_merge_train')).toBe('Eligible for merge train');
    expect(verbPhrase('rejected')).toBe('Rejected');
    expect(verbPhrase('deferred')).toBe('Deferred');
  });

  it('never produces banned approval wording', () => {
    for (const verb of ['eligible_for_merge_train', 'rejected', 'deferred']) {
      expect(verbPhrase(verb).toLowerCase()).not.toContain('approved');
      expect(findBannedPhrases(verbPhrase(verb))).toEqual([]);
    }
  });

  it('passes unknown verbs through raw instead of inventing copy', () => {
    expect(verbPhrase('combine')).toBe('combine');
  });
});

describe('banned phrases (T4 lint fixture)', () => {
  // Frozen enumeration from PRODUCT_UX_PLAN §4 T4 — changes require a ruling.
  it('matches the plan list exactly', () => {
    expect([...BANNED_PHRASES]).toEqual([
      'guaranteed',
      'fully verified',
      'safe to merge',
      'all tests pass',
      'ci green',
    ]);
  });

  it.each([...BANNED_PHRASES])('detects "%s" case-insensitively inside longer copy',
    (phrase) => {
      const copy = `This change is ${phrase.toUpperCase()} per our pipeline.`;
      expect(findBannedPhrases(copy)).toEqual([phrase]);
    },
  );

  it('passes clean calibrated copy (the sanctioned phrasings)', () => {
    expect(
      findBannedPhrases(
        'Eligible under pol_payments_high_risk v4 · required evidence accepted · 3 suites deferred with reasons',
      ),
    ).toEqual([]);
  });

  it('finds multiple violations when copy stacks them', () => {
    expect(findBannedPhrases('CI green and fully verified — safe to merge')).toEqual([
      'fully verified',
      'safe to merge',
      'ci green',
    ]);
  });
});
