// Permalink query parsing for /candidates/[id] (plan §2.5, §3.2): the canonical
// form is ?at=dec_…&src=gh_check. Unknown params are ignored entirely; a
// malformed known value degrades independently — a broken pin never suppresses
// the context chip, and attacker-controlled strings never reach the DOM.
import { z } from 'zod';
import { candidateIdSchema, decisionIdSchema } from './api-schemas';

const ghCheckSrcSchema = z.literal('gh_check');

function firstString(value: string | string[] | undefined): string | undefined {
  if (Array.isArray(value)) return typeof value[0] === 'string' ? value[0] : undefined;
  return value;
}

// Bridge from the browser's useSearchParams() API to the record shape this
// module validates. WHY here: the client-era candidate shell reads params via
// hooks; keeping the adapter beside parsePermalinkParams means repeated-value
// and absent-key semantics can never drift between the two entry styles.
export function urlSearchParamsToRecord(
  params: URLSearchParams,
): Record<string, string | string[]> {
  const out: Record<string, string | string[]> = {};
  params.forEach((value, key) => {
    const existing = out[key];
    if (existing === undefined) {
      out[key] = value;
      return;
    }
    if (Array.isArray(existing)) existing.push(value);
    else out[key] = [existing, value];
  });
  return out;
}

export interface PermalinkParams {
  // dec_* id when the URL pins a decision AND the value is well-formed.
  pinnedDecisionId: string | null;
  fromGithubCheck: boolean;
}

// Next.js hands searchParams through as string | string[] | undefined; taking
// only the first element of an array keeps repeated params from smuggling a
// second interpretation past validation.
export function parsePermalinkParams(
  searchParams: Record<string, string | string[] | undefined>,
): PermalinkParams {
  const atResult = decisionIdSchema.safeParse(firstString(searchParams.at));
  const srcResult = ghCheckSrcSchema.safeParse(firstString(searchParams.src));
  return {
    pinnedDecisionId: atResult.success ? atResult.data : null,
    fromGithubCheck: srcResult.success,
  };
}

// Canonical shareable path written to the clipboard by CopyEvidenceLink.
// Inputs are re-validated here so a rendering bug upstream can never mint a
// malformed link (fail-closed mirrors the API client).
export function evidencePermalinkPath(candidateId: string, decisionId: string): string {
  if (!candidateIdSchema.safeParse(candidateId).success) {
    throw new TypeError(`evidencePermalinkPath: invalid candidate id ${candidateId}`);
  }
  if (!decisionIdSchema.safeParse(decisionId).success) {
    throw new TypeError(`evidencePermalinkPath: invalid decision id ${decisionId}`);
  }
  return `/candidates/${candidateId}?at=${decisionId}`;
}
