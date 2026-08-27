// Gateway handler contract tests with a fake upstream (SPEC §3 2026-08-26):
// cookie set on login token capture, NO cookie on auth failures, admin
// bearer withheld from auth paths / injected elsewhere, cookie→bearer
// translation for /me.
import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import { NextRequest } from 'next/server';

const originalFetch = globalThis.fetch;

async function loadHandler() {
  return await import('./route');
}

function fakeUpstream(status: number, body: unknown): void {
  globalThis.fetch = (async () =>
    new Response(JSON.stringify(body), {
      status,
      headers: { 'Content-Type': 'application/json' },
    })) as typeof fetch;
}

function postRequest(path: string, bodyJson: unknown): NextRequest {
  return new NextRequest(`https://console.example.com${path}`, {
    method: 'POST',
    body: JSON.stringify(bodyJson),
    headers: { 'Content-Type': 'application/json' },
  });
}

beforeEach(() => {
  // Module-level constant; must exist before ./route is first imported.
  process.env.CISYNC_ADMIN_TOKEN ??= 'test_admin_token_from_vitest';
});

afterEach(() => {
  globalThis.fetch = originalFetch;
});

describe('gateway /v1/auth/* special-case', () => {
  it('login 200 upstream → Set-Cookie httpOnly session jar minted from token', async () => {
    const { GET, POST } = await loadHandler();
    let seenUrl = '';
    globalThis.fetch = (async (input: RequestInfo | URL) => {
      seenUrl = String(input);
      return new Response(
        JSON.stringify({ token: 'jwt-xyz', user: { email: 'dev@example.com' } }),
        { status: 200, headers: { 'Content-Type': 'application/json' } },
      );
    }) as typeof fetch;

    const res = await POST(
      postRequest('/api/cisync/v1/auth/login', { email: 'dev@example.com', password: 'long-enough-pass' }),
    );

    expect(seenUrl).toBe('http://control-plane:8081/v1/auth/login');
    expect(res.status).toBe(200);
    const cookie = res.headers.get('Set-Cookie') ?? '';
    expect(cookie).toContain('cisync_session=jwt-xyz');
    expect(cookie).toContain('HttpOnly');
    expect(cookie).toContain('Max-Age=');
    await expect(res.json()).resolves.toMatchObject({ user: { email: 'dev@example.com' } });
    void GET;
  });

  it('auth failure responses never mint cookies', async () => {
    const { POST } = await loadHandler();
    fakeUpstream(401, { error: { code: 'invalid_credentials', message: 'invalid email or password' } });
    const res = await POST(
      postRequest('/api/cisync/v1/auth/login', { email: 'dev@example.com', password: 'nope-nope-nope' }),
    );
    expect(res.status).toBe(401);
    expect(res.headers.get('Set-Cookie')).toBeNull();
    await expect(res.json()).resolves.toMatchObject({ error: { code: 'invalid_credentials' } });
  });

  it('signup 201 without token passes through without minting', async () => {
    const { POST } = await loadHandler();
    fakeUpstream(201, { user: { email: 'dev@example.com' } });
    const res = await POST(
      postRequest('/api/cisync/v1/auth/signup', { email: 'dev@example.com', password: 'long-enough-pass' }),
    );
    expect(res.status).toBe(201);
    expect(res.headers.get('Set-Cookie')).toBeNull();
  });

  it('/me translates session cookie → upstream Authorization bearer, drops Cookie', async () => {
    const { GET } = await loadHandler();
    let seenHeadersJson: Record<string, string | null> = {};
    let seenUrl = '';
    globalThis.fetch = (async (input: RequestInfo | URL, init?: RequestInit) => {
      seenUrl = String(input);
      seenHeadersJson = Object.fromEntries(new Headers(init?.headers).entries());
      return new Response(JSON.stringify({ user: { email: 'dev@example.com' } }), { status: 200 });
    }) as typeof fetch;

    const req = new NextRequest('https://console.example.com/api/cisync/v1/auth/me', {
      method: 'GET',
      headers: { Cookie: 'cisync_session=jwt-abc; theme=dark' },
    });
    const res = await GET(req);

    expect(seenUrl).toBe('http://control-plane:8081/v1/auth/me');
    expect(seenHeadersJson['authorization']).toBe('Bearer jwt-abc');
    // WHY not toHaveProperty: Headers dropped the value entirely — asserting
    // absence distinguishes "deleted" from "empty string".
    expect(seenHeadersJson.cookie).toBeUndefined();
    expect(await res.json()).toMatchObject({ user: { email: 'dev@example.com' } });
  });

  it('admin bearer IS injected for non-auth paths (regression guard)', async () => {
    const { GET } = await loadHandler();
    let seenAuth: string | null = null;
    globalThis.fetch = (async (_u: unknown, init?: RequestInit) => {
      seenAuth = new Headers(init?.headers).get('Authorization');
      return new Response('{}', { status: 200 });
    }) as typeof fetch;

    await GET(new NextRequest('https://console.example.com/api/cisync/v1/change-intents/x'));
    expect(seenAuth ?? '').toContain('Bearer test_admin_token_from_vitest');
  });

  it('upstream unreachable keeps the 503 unavailable contract', async () => {
    const { GET } = await loadHandler();
    globalThis.fetch = (async () => {
      throw new TypeError('connect ECONNREFUSED');
    }) as typeof fetch;
    const res = await GET(new NextRequest('https://console.example.com/api/cisync/v1/events'));
    expect(res.status).toBe(503);
    await expect(res.json()).resolves.toMatchObject({
      error: { code: 'unavailable' },
    });
  });
});
