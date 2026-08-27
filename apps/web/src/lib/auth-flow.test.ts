// Auth flow contract (email+password, SPEC §3 2026-08-26): mocked-fetch
// signup→login journey plus the error-code matrix. WHY no DOM: the form's
// render contract is pinned in login-form.test.tsx; here we exercise pure
// network transitions only.
import { describe, expect, it } from 'vitest';
import { logIn, signUp, type FetchLike } from './auth-flow';

function jsonResponse(status: number, body: unknown): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });
}

interface RecordingFetcher extends FetchLike {
  calls: Array<{ url: string; init?: RequestInit }>;
}

function mockFetch(impl: (url: string) => Promise<Response>): RecordingFetcher {
  const calls: Array<{ url: string; init?: RequestInit }> = [];
  // WHY async arrow with explicit Promise return: satisfies FetchLike's
  // Promise<Response> under strict TS without leaning on inference.
  const fn = async (url: string, init?: RequestInit): Promise<Response> => {
    calls.push({ url, init });
    return await impl(url);
  };
  return Object.assign(fn, { calls });
}

describe('signup → login journey (mocked fetch)', () => {
  it('posts signup then login through the same-origin gateway', async () => {
    const fetcher = mockFetch(async (url) =>
      url.endsWith('/auth/signup')
        ? jsonResponse(201, { user: { email: 'dev@example.com' } })
        : jsonResponse(200, { token: 'jwt-for-upstream-only', user: { email: 'dev@example.com' } }),
    );

    const up = await signUp('dev@example.com', 'correct-horse-42', fetcher);
    expect(up).toEqual({ ok: true });

    const inn = await logIn('dev@example.com', 'correct-horse-42', fetcher);
    expect(inn).toEqual({ ok: true });

    expect(fetcher.calls.map((c) => c.url)).toEqual([
      '/api/cisync/v1/auth/signup',
      '/api/cisync/v1/auth/login',
    ]);
    // Passwords travel as JSON bodies; no Authorization header is forged
    // client-side (gateway injects nothing for public auth paths).
    for (const call of fetcher.calls) {
      const headers = new Headers(call.init?.headers);
      expect(headers.get('Authorization')).toBeNull();
      const parsedBody: unknown = JSON.parse(String(call.init?.body));
      expect(parsedBody).toHaveProperty('password');
      expect(parsedBody).toMatchObject({ email: 'dev@example.com' });
    }
  });
});

describe('error-code matrix', () => {
  it.each([
    [400, 'weak_password'],
    [400, 'invalid_email'],
    [401, 'invalid_credentials'],
    [409, 'exists'],
  ] as const)('%d %s maps to its error kind', async (status, code) => {
    const fetcher = mockFetch(async () => jsonResponse(status, { error: { code, message: code } }));
    const result = await logIn('dev@example.com', 'whatever-pass-123', fetcher);
    expect(result).toMatchObject({ ok: false, error: { kind: code, message: code } });
  });

  it('rate_limited carries retry_after_s', async () => {
    const fetcher = mockFetch(async () =>
      jsonResponse(429, { error: { code: 'rate_limited', retry_after_s: 37 } }),
    );
    const result = await logIn('dev@example.com', 'whatever-pass-123', fetcher);
    expect(result).toEqual({
      ok: false,
      error: { kind: 'rate_limited', message: 'rate_limited', retryAfterS: 37 },
    });
  });

  it('non-conforming success bodies degrade to server, never to success', async () => {
    const fetcher = mockFetch(async () => jsonResponse(200, { totally: 'unexpected' }));
    const result = await logIn('dev@example.com', 'whatever-pass-123', fetcher);
    expect(result).toMatchObject({ ok: false, error: { kind: 'server' } });

    const signupBad = await signUp('x@y.z', 'whatever-pass-123', mockFetch(async () => jsonResponse(201, { junk: true })));
    expect(signupBad).toMatchObject({ ok: false, error: { kind: 'server' } });
  });

  it('network failures map to the network kind', async () => {
    const fetcher: FetchLike = async () => {
      throw new TypeError('fetch failed');
    };
    const result = await logIn('dev@example.com', 'whatever-pass-123', fetcher);
    expect(result).toEqual({ ok: false, error: { kind: 'network', message: 'network unreachable' } });
  });
});
