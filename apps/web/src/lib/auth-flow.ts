// Client-side auth call layer for /login. WHY a separate module: the form
// component stays presentational while every network transition is a pure,
// stubbable function — the vitest suite exercises signup→login→me with a
// mocked fetch and no DOM (email+password flow per SPEC §3 2026-08-26;
// the OTP code path is deleted).
//
// Requests go through the SAME same-origin gateway as every other console
// call (/api/cisync/v1/auth/*): the route handler special-cases auth paths to
// capture the upstream session JWT and set the httpOnly cisync_session
// cookie server-side. The token itself never crosses the client JS boundary.

import { z } from 'zod';

export type FetchLike = (input: string, init?: RequestInit) => Promise<Response>;

export type AuthStepError = {
  kind:
    | 'network'
    | 'invalid_credentials' // uniform 401 body (no enumeration)
    | 'weak_password'
    | 'invalid_email'
    | 'exists'
    | 'rate_limited'
    | 'server';
  message: string;
  retryAfterS?: number;
};

const userSchema = z.object({ email: z.string() });
const successSchema = z.object({ user: userSchema });
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
    case 'invalid_credentials':
      return { kind: 'invalid_credentials', message };
    case 'weak_password':
      return { kind: 'weak_password', message };
    case 'invalid_email':
      return { kind: 'invalid_email', message };
    case 'exists':
      return { kind: 'exists', message };
    case 'rate_limited':
      return {
        kind: 'rate_limited',
        message,
        retryAfterS: parsed.success ? (parsed.data.error.retry_after_s ?? undefined) : undefined,
      };
    default:
      return { kind: 'server', message: `${message} (HTTP ${status})` };
  }
}

function assertPayload(body: unknown): AuthStepError | null {
  return successSchema.safeParse(body).success ? null : { kind: 'server', message: 'unexpected response shape' };
}

export async function signUp(
  email: string,
  password: string,
  fetchImpl: FetchLike,
): Promise<{ ok: true } | { ok: false; error: AuthStepError }> {
  let response: Response;
  try {
    response = await fetchImpl('/api/cisync/v1/auth/signup', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ email, password }),
    });
  } catch {
    return { ok: false, error: { kind: 'network', message: 'network unreachable' } };
  }
  if (!response.ok) return { ok: false, error: mapError(response.status, await parseJson(response)) };
  // Fail-closed even on 201s: any non-conforming confirmation body is treated
  // as a service defect. Cookie still comes from the LOGIN call below.
  const err = assertPayload(await parseJson(response));
  return err !== null ? { ok: false, error: err } : { ok: true };
}

export async function logIn(
  email: string,
  password: string,
  fetchImpl: FetchLike,
): Promise<{ ok: true } | { ok: false; error: AuthStepError }> {
  let response: Response;
  try {
    response = await fetchImpl('/api/cisync/v1/auth/login', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ email, password }),
    });
  } catch {
    return { ok: false, error: { kind: 'network', message: 'network unreachable' } };
  }
  if (!response.ok) return { ok: false, error: mapError(response.status, await parseJson(response)) };
  // Upstream {token,user} is consumed SERVER-side by the gateway handler
  // (cookie captured there); the client contract is just success + user echo.
  const err = assertPayload(await parseJson(response));
  return err !== null ? { ok: false, error: err } : { ok: true };
}
