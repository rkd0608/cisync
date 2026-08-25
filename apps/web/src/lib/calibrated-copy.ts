// T4 calibrated language rules (docs/plans/PRODUCT_UX_PLAN.md §4), binding for
// every decision surface: confidence words always travel with their numbers,
// ledger verbs render verbatim (never "Approved"), and the banned-phrase list
// is exported so fixture tests can lint rendered copy.

export type ConfidenceWord = 'high' | 'moderate' | 'low' | 'insufficient';

// Plan §4 T4: ≥0.95 high · 0.80–0.94 moderate · 0.50–0.79 low · else insufficient.
export const CONFIDENCE_THRESHOLDS = {
  high: 0.95,
  moderate: 0.8,
  low: 0.5,
} as const;

function isUnitInterval(value: number): boolean {
  return Number.isFinite(value) && value >= 0 && value <= 1;
}

export function confidenceWord(confidence: number): ConfidenceWord {
  // Out-of-domain numbers are not "high confidence" — they are unusable input.
  if (!isUnitInterval(confidence)) return 'insufficient';
  if (confidence >= CONFIDENCE_THRESHOLDS.high) return 'high';
  if (confidence >= CONFIDENCE_THRESHOLDS.moderate) return 'moderate';
  if (confidence >= CONFIDENCE_THRESHOLDS.low) return 'low';
  return 'insufficient';
}

export interface CalibratedConfidence {
  word: ConfidenceWord;
  pct: string;
  label: string;
}

// T4: numbers ALWAYS alongside words — e.g. "moderate · 94.0% confidence".
export function calibratedConfidence(confidence: number): CalibratedConfidence {
  const word = confidenceWord(confidence);
  const pct = isUnitInterval(confidence) ? `${(confidence * 100).toFixed(1)}%` : '--';
  return { word, pct, label: `${word} · ${pct} confidence` };
}

export const DECISION_VERBS = [
  'eligible_for_merge_train',
  'rejected',
  'deferred',
] as const;
export type DecisionVerb = (typeof DECISION_VERBS)[number];

const VERB_PHRASES: Record<DecisionVerb, string> = {
  eligible_for_merge_train: 'Eligible for merge train',
  rejected: 'Rejected',
  deferred: 'Deferred',
};

// Verbs mirror ledger verbs exactly (T4); an unknown verb renders raw rather
// than being paraphrased into something the ledger never said.
export function verbPhrase(verb: string): string {
  return VERB_PHRASES[verb as DecisionVerb] ?? verb;
}

// Banned reassurance phrases (T4). The lint fixture asserts this list matches
// the plan enumeration exactly; findBannedPhrases scans rendered copy.
export const BANNED_PHRASES = [
  'guaranteed',
  'fully verified',
  'safe to merge',
  'all tests pass',
  'ci green',
] as const;

export function findBannedPhrases(text: string): string[] {
  const haystack = text.toLowerCase();
  return BANNED_PHRASES.filter((phrase) => haystack.includes(phrase));
}
