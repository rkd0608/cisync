// Gateway auth contract tests (fake upstream, no Next server): token→cookie
// mint, clear-cookie and cookie parse byte-contracts shared by the route
// handler and middleware (SPEC §3 2026-08-26).
import { describe, expect, it } from 'vitest';
import {
  extractCookie,
  extractSessionToken,
  sessionClearCookie,
  sessionCookieFromUpstream,
  SESSION_COOKIE_NAME,
} from './gateway-auth';

describe('extractSessionToken (fail-closed)', () => {
  it.each([
    [{ token: 'abc.def.ghi' }, 'abc.def.ghi'],
    [{ token: 'a', user: { email: 'x@y.z' } }, 'a'],
    [null, null],
    [undefined, null],
    ['string', null],
    [{}, null],
    [{ token: '' }, null],
    [{ token: 'two tokens' }, null], // whitespace ⇒ tampering risk
    [{ token: 42 }, null],
  ])('payload %j → %j', (payload, expected) => {
    expect(extractSessionToken(payload)).toBe(expected);
  });
});

describe('session cookie mint/clear contract', () => {
  const secure = { secure: true };
  const insecure = { secure: false };

  it('mints httpOnly SameSite=Lax cookie with Max-Age 30d', () => {
    const header = sessionCookieFromUpstream({ token: 'jwt-1', extra: true }, secure);
    expect(header).toContain(`${SESSION_COOKIE_NAME}=jwt-1`);
    expect(header).toContain('HttpOnly');
    expect(header).toContain('SameSite=Lax');
    expect(header).toContain(`Max-Age=${30 * 24 * 60 * 60}`);
    expect(header).toContain('Secure');
  });

  it('omits Secure outside production so local http works', () => {
    const header = sessionCookieFromUpstream({ token: 'jwt-2' }, insecure);
    expect(header).not.toContain('Secure');
  });

  it('returns null when upstream payload has no token — never an empty cookie', () => {
    expect(sessionCookieFromUpstream({}, secure)).toBeNull();
    expect(sessionCookieFromUpstream({ user: {} }, secure)).toBeNull();
  });

  it('clear cookie expires the jar with empty value + Max-Age 0', () => {
    const cleared = sessionClearCookie(secure);
    expect(cleared.startsWith(`${SESSION_COOKIE_NAME}=`)).toBe(true);
    expect(cleared).toContain('Max-Age=0');
    expect(cleared).toContain('HttpOnly');
  });
});

describe('extractCookie parsing', () => {
  it('reads the named cookie among many', () => {
    expect(extractCookie('theme=dark; cisync_session=jwt-9; other=x', SESSION_COOKIE_NAME)).toBe('jwt-9');
    expect(extractCookie(`cisync_session=${'x'.repeat(10)}`, SESSION_COOKIE_NAME)).toBe('x'.repeat(10));
  });

  it('handles absence and empties without throwing', () => {
    expect(extractCookie(undefined, SESSION_COOKIE_NAME)).toBeUndefined();
    expect(extractCookie('', SESSION_COOKIE_NAME)).toBeUndefined();
    expect(extractCookie('cisync_session=', SESSION_COOKIE_NAME)).toBeUndefined();
    expect(extractCookie('garbage-no-equals; another=one', SESSION_COOKIE_NAME)).toBeUndefined();
  });
});
