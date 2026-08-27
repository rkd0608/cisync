import { describe, expect, it } from 'vitest';
import {
  evidencePermalinkPath,
  parsePermalinkParams,
  urlSearchParamsToRecord,
} from './permalink-params';

const VALID_DEC = 'dec_01J8ZC7XK9Q2W3E4R5T6Y70003';
const VALID_CAND = 'cand_01J8ZC7XK9Q2W3E4R5T6Y70002';

describe('parsePermalinkParams', () => {
  it('accepts the canonical ?at + &src form', () => {
    const parsed = parsePermalinkParams({ at: VALID_DEC, src: 'gh_check' });
    expect(parsed).toEqual({ pinnedDecisionId: VALID_DEC, fromGithubCheck: true });
  });

  it('treats a missing at as "no pin requested"', () => {
    expect(parsePermalinkParams({})).toEqual({
      pinnedDecisionId: null,
      fromGithubCheck: false,
    });
  });

  it('strips unknown params entirely (utm pollution, tracking junk)', () => {
    const parsed = parsePermalinkParams({
      at: VALID_DEC,
      utm_source: 'slack',
      fbclid: 'x'.repeat(64),
      redirect: 'https://evil.example',
    });
    // Only known keys survive; nothing else is even readable off the result.
    expect(parsed).toEqual({ pinnedDecisionId: VALID_DEC, fromGithubCheck: false });
    expect(Object.keys(parsed).sort()).toEqual(['fromGithubCheck', 'pinnedDecisionId']);
  });

  it('drops malicious at values instead of rendering them', () => {
    for (const hostile of [
      '<script>alert(1)</script>',
      `dec_${'A'.repeat(25)}; DROP TABLE decisions`,
      'javascript:alert(1)',
      `${VALID_DEC}//../../intents`,
      'dec_%00',
      '',
    ]) {
      expect(parsePermalinkParams({ at: hostile }).pinnedDecisionId).toBeNull();
    }
  });

  it('rejects lowercase and wrong-prefix decision ids (ULID charset)', () => {
    expect(parsePermalinkParams({ at: `dec_${'a'.repeat(26)}` }).pinnedDecisionId).toBeNull();
    expect(parsePermalinkParams({ at: `cand_${'A'.repeat(26)}` }).pinnedDecisionId).toBeNull();
    expect(parsePermalinkParams({ at: VALID_DEC.toLowerCase() }).pinnedDecisionId).toBeNull();
  });

  it('keeps src=gh_check working when at is broken (independent degradation)', () => {
    const parsed = parsePermalinkParams({ at: 'not-a-decision', src: 'gh_check' });
    expect(parsed.pinnedDecisionId).toBeNull();
    expect(parsed.fromGithubCheck).toBe(true);
  });

  it('accepts only the exact gh_check literal for src', () => {
    expect(parsePermalinkParams({ src: 'gh_check ' }).fromGithubCheck).toBe(false);
    expect(parsePermalinkParams({ src: 'GH_CHECK' }).fromGithubCheck).toBe(false);
    expect(parsePermalinkParams({ src: 'web' }).fromGithubCheck).toBe(false);
  });

  it('uses only the first value of a repeated param (no array smuggling)', () => {
    const parsed = parsePermalinkParams({ at: [VALID_DEC, '<svg/onload=alert(1)>'] });
    expect(parsed.pinnedDecisionId).toBe(VALID_DEC);
  });

  it('treats an empty repeated param as absent', () => {
    const parsed = parsePermalinkParams({ at: [] });
    expect(parsed.pinnedDecisionId).toBeNull();
  });
});

// Client shells (B2 SSR fix) read the URL with useSearchParams() inside a
// client component; this adapter bridges that API to the record shape
// parsePermalinkParams was hardened against. Pinning its contract here keeps
// repeated/absent handling byte-compatible with the server-era behavior.
describe('urlSearchParamsToRecord', () => {
  const make = (query: string): URLSearchParams => new URLSearchParams(query);

  it('maps single values as plain strings', () => {
    expect(urlSearchParamsToRecord(make('at=dec_x&src=gh_check'))).toEqual({
      at: 'dec_x',
      src: 'gh_check',
    });
  });

  it('folds repeats into arrays so first-value-wins survives downstream', () => {
    expect(urlSearchParamsToRecord(make('at=a&at=b&src=x'))).toEqual({
      at: ['a', 'b'],
      src: 'x',
    });
  });

  it('returns an empty record for an empty query', () => {
    expect(urlSearchParamsToRecord(make(''))).toEqual({});
  });
});

describe('evidencePermalinkPath', () => {
  it('builds the canonical pinned path', () => {
    expect(evidencePermalinkPath(VALID_CAND, VALID_DEC)).toBe(
      `/candidates/${VALID_CAND}?at=${VALID_DEC}`,
    );
  });

  it('refuses to mint links from invalid ids (defense in depth)', () => {
    expect(() => evidencePermalinkPath('../etc/passwd', VALID_DEC)).toThrow(TypeError);
    expect(() => evidencePermalinkPath(VALID_CAND, 'dec_short')).toThrow(TypeError);
  });
});
