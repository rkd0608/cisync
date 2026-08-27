// Client-side auth call layer for /login. WHY a separate module: the form
// component stays presentational while every network transition is a pure,
// stubbable function — the vitest suite exercises the flow with mocked fetch
// and no DOM.

import { z } from 'zod';

export type FetchLike = (input: string, init?: RequestInit) => Promise<Response>;

export type AuthStepError = {
  kind: 'network' | 'not_allowed' | 'rate_limited' | 'invalid_code' | 'server';
  message: string;
  retryAfterS?: number;
};

const requestCodeResponseSchema = z.object({ ok: z.literal(true) }).passthrough();
const errorResponseSchema = z
  .object({
    error: z.object({
      code: z.string(),
      message: z.string().optional(),
      retry_after_s: z.number().optional(),
    }),
  })
  .passthrough();

async function parseJson(response: Response): Promise<unknown> {
  try {
    return await response.json();
  } catch {
    return null;
  }
}

// Fail-closed mapping: any non-conforming response degrades to `server` with
// the HTTP status, never to a false success.
function mapError(status: number, body: unknown): AuthStepError {
  const parsed = errorResponseSchema.safeParse(body);
  const code = parsed.success ? parsed.data.error.code : 'unparseable_error';
  const message = parsed.success ? (parsed.data.error.message ?? code) : `HTTP ${status}`;
  switch (code) {
    case 'not_allowed':
      return { kind: 'not_allowed', message };
    case 'rate_limited':
      return {
        kind: 'rate_limited',
        message,
        retryAfterS: parsed.success ? (parsed.data.error.retry_after_s ?? undefined) : undefined,
      };
    case 'invalid_code':
      return { kind: 'invalid_code', message };
    default:
      return { kind: 'server', message: `${message} (HTTP ${status})` };
  }
}

export async function requestCode(
  email: string,
  fetchImpl: FetchLike,
): Promise<{ ok: true } | { ok: false; error: AuthStepError }> {
  let response: Response;
  try {
    response = await fetchImpl('/api/auth/request-code', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ email }),
    });
  } catch {
    return { ok: false, error: { kind: 'network', message: 'network unreachable' } };
  }
  if (!response.ok) return { ok: false, error: mapError(response.status, await parseJson(response)) };
  if (!requestCodeResponseSchema.safeParse(await parseJson(response)).success) {
    return { ok: false, error: { kind: 'server', message: 'unexpected response shape' } };
  }
  return { ok: true };
}

export async function verifyCode(
  email: string,
  code: string,
  fetchImpl: FetchLike,
): Promise<{ ok: true } | { ok: false; error: AuthStepError }> {
  let response: Response;
  try {
    response = await fetchImpl('/api/auth/verify', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ email, code }),
    });
  } catch {
    return { ok: false, error: { kind: 'network', message: 'network unreachable' } };
  }
  if (!response.ok) return { ok: false, error: mapError(response.status, await parseJson(response)) };
  return { ok: true };
}
