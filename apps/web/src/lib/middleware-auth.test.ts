// Oracle contract tests: valid cookie → authenticated with email echo;
// tampered/stale/absent/unreachable → unauthenticated, uniformly (middleware
// maps all of these to the same /login?next= bounce).
import { describe, expect, it } from 'vitest';
import { checkSessionViaGateway } from './middleware-auth';

const ORIGIN = 'https://console.example.com';

function fetchReturning(status: number, body?: unknown): typeof fetch {
  return async () =>
    new Response(body === undefined ? '' : JSON.stringify(body), {
      status,
      headers: { 'Content-Type': 'application/json' },
    });
}

describe('checkSessionViaGateway', () => {
  it('forwards ONLY the session cookie to the same-origin gateway path', async () => {
    let seenUrl = '';
    // WHY Record<string,string> capture instead of Headers typing directly:
    // reading .get off a null-narrowed variable trips TS in this callback
    // shape; we only need one header value.
    let seenCookieHeader: string | null = null;
    const impl = (async (input: RequestInfo | URL, init?: RequestInit) => {
      seenUrl = String(input);
      seenCookieHeader = new Headers(init?.headers).get('cookie');
      return new Response(JSON.stringify({ user: { email: 'dev@example.com' } }), { status: 200 });
    }) as typeof fetch;

    const result = await checkSessionViaGateway(ORIGIN, 'cisync_session=jwt-abc; theme=dark', impl);

    expect(result).toEqual({ authenticated: true, email: 'dev@example.com' });
    expect(seenUrl).toBe(`${ORIGIN}/api/cisync/v1/auth/me`);
    // Cookie forwarded verbatim; no forged Authorization.
    expect(seenCookieHeader).toBe('cisync_session=jwt-abc; theme=dark');
  });

  it.each([
    [401, undefined], // tampered/stale cookie
    [403, undefined],
    [500, undefined], // upstream crash
    [200, { wrongShape: true }], // contract drift — fail closed
    [200, { user: {} }],
  ] as const)('upstream %j/%j ⇒ unauthenticated', async (status, body) => {
    const result = await checkSessionViaGateway(ORIGIN, 'cisync_session=x', fetchReturning(status, body));
    expect(result).toEqual({ authenticated: false, email: null });
  });

  it('absent cookie still calls the oracle (upstream must see it is anonymous)', async () => {
    let sawCookie = '';
    const impl = (async (_u: unknown, init?: RequestInit) => {
      sawCookie = String(new Headers(init?.headers).get('cookie'));
      return new Response('', { status: 401 });
    }) as typeof fetch;
    const result = await checkSessionViaGateway(ORIGIN, undefined, impl);
    expect(result.authenticated).toBe(false);
    expect(sawCookie).toBe('null');
  });

  it('network failure maps to unauthenticated (never an auth bypass)', async () => {
    const broken = (async () => {
      throw new TypeError('fetch failed');
    }) as typeof fetch;
    expect(await checkSessionViaGateway(ORIGIN, 'cisync_session=x', broken)).toEqual({
      authenticated: false,
      email: null,
    });
  });
});
