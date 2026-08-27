import { describe, expect, it } from 'vitest';
import { requestCode, verifyCode, type FetchLike } from './login-flow';

function jsonResponse(status: number, body: unknown): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });
}

describe('requestCode over mock fetch', () => {
  it('sends POST JSON to /api/auth/request-code and succeeds on ok:true', async () => {
    let capturedUrl = '';
    let capturedInit: RequestInit | undefined;
    const fetchImpl: FetchLike = async (input, init) => {
      capturedUrl = input;
      capturedInit = init;
      return jsonResponse(200, { ok: true });
    };
    await expect(requestCode('dev@yourdomain.com', fetchImpl)).resolves.toEqual({ ok: true });
    expect(capturedUrl).toBe('/api/auth/request-code');
    expect(capturedInit?.method).toBe('POST');
    expect(String(capturedInit?.body)).toContain('dev@yourdomain.com');
  });

  it.each([
    [403, { error: { code: 'not_allowed', message: 'email not allowlisted' } }, 'not_allowed'],
    [
      429,
      { error: { code: 'rate_limited', retry_after_s: 42, message: 'slow down' } },
      'rate_limited',
    ],
    [500, { error: { code: 'server_misconfigured', message: 'missing AUTH_SECRET' } }, 'server'],
    [200, { nonsense: true }, 'server'],
  ] as const)('maps HTTP %i %j to %s failure', async (status, body, expectedKind) => {
    const result = await requestCode('dev@yourdomain.com', () =>
      Promise.resolve(jsonResponse(status, body)),
    );
    expect(result.ok).toBe(false);
    if (!result.ok) expect(result.error.kind).toBe(expectedKind);
  });

  it('maps rate-limit bodies through to the retry hint', async () => {
    const result = await requestCode('dev@yourdomain.com', () =>
      Promise.resolve(
        jsonResponse(429, { error: { code: 'rate_limited', retry_after_s: 42, message: 'slow down' } }),
      ),
    );
    expect(result.ok).toBe(false);
    if (!result.ok && result.error.kind === 'rate_limited') expect(result.error.retryAfterS).toBe(42);
  });

  it('surfaces network failures distinctly from HTTP errors', async () => {
    const result = await requestCode('dev@yourdomain.com', () => Promise.reject(new Error('offline')));
    expect(result.ok).toBe(false);
    if (!result.ok) expect(result.error.kind).toBe('network');
  });
});

describe('verifyCode over mock fetch', () => {
  it('posts email + code and succeeds on HTTP 200', async () => {
    let capturedBody = '';
    const fetchImpl: FetchLike = async (_input, init) => {
      capturedBody = String(init?.body);
      return jsonResponse(200, { ok: true, email: 'dev@yourdomain.com' });
    };
    await expect(verifyCode('dev@yourdomain.com', '123456', fetchImpl)).resolves.toEqual({ ok: true });
    expect(capturedBody).toContain('"123456"');
  });

  it('maps an invalid/expired code rejection to invalid_code', async () => {
    const result = await verifyCode('dev@yourdomain.com', '999999', () =>
      Promise.resolve(jsonResponse(400, { error: { code: 'invalid_code', message: 'bad or expired' } })),
    );
    expect(result.ok).toBe(false);
    if (!result.ok) expect(result.error.kind).toBe('invalid_code');
  });

  it('treats a network failure as retryable, not as a wrong code', async () => {
    const result = await verifyCode('dev@yourdomain.com', '123456', () =>
      Promise.reject(new Error('offline')),
    );
    expect(result.ok).toBe(false);
    if (!result.ok) expect(result.error.kind).toBe('network');
  });
});
