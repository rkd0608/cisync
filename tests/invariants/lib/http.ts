import type { ZodType } from 'zod';

/**
 * Validated HTTP layer for black-box probes.
 * WHY: the charter forbids trusting incoming data — every response body is
 * parsed against a zod schema before assertions ever see it; servers that
 * drift from openapi.yaml fail here with a precise message instead of
 * producing confusing downstream assertion errors.
 */

export interface HttpResponse<T> {
  status: number;
  ok: boolean;
  body: T;
  /** Raw text retained because idempotent-replay equality is byte-level (I-12). */
  rawText: string;
}

export class HttpError extends Error {
  constructor(
    public readonly status: number,
    public readonly rawBody: string,
    detail: string,
  ) {
    super(`HTTP ${status}: ${detail} :: ${rawBody.slice(0, 300)}`);
  }
}

export interface RequestOptions {
  url: string;
  method: 'GET' | 'POST' | 'DELETE';
  headers?: Record<string, string>;
  body?: unknown;
  signal?: AbortSignal;
}

/**
 * WHY retry here: under a 24-way concurrent burst the Node fetch pool can
 * drop connections (ECONNRESET surfaces as TypeError before any response
 * exists). That is transport noise, not a server verdict — I-06 must judge
 * typed HTTP statuses only. Retries are therefore limited to thrown
 * connection-class errors, 2 attempts total, never to actual HTTP responses.
 */
const CONNECTION_RETRY_ATTEMPTS = 2;
const CONNECTION_RETRY_BACKOFF_MS = 150;

function isConnectionClassError(err: unknown): boolean {
  return err instanceof TypeError && !('status' in err);
}

async function fetchWithConnectionRetry(opts: RequestOptions): Promise<Response> {
  let lastError: unknown;
  for (let attempt = 1; attempt <= CONNECTION_RETRY_ATTEMPTS; attempt++) {
    try {
      return await fetch(opts.url, {
        method: opts.method,
        headers: opts.headers,
        body: opts.body === undefined ? undefined : JSON.stringify(opts.body),
        signal: opts.signal,
      });
    } catch (err) {
      lastError = err;
      if (!isConnectionClassError(err) || attempt === CONNECTION_RETRY_ATTEMPTS) throw err;
      await new Promise((r) => setTimeout(r, CONNECTION_RETRY_BACKOFF_MS));
    }
  }
  throw lastError;
}

export async function request<T>(opts: RequestOptions, schema: ZodType<T>): Promise<HttpResponse<T>> {
  const res = await fetchWithConnectionRetry(opts);
  const rawText = await res.text();
  let parsedUnknown: unknown;
  try {
    parsedUnknown = rawText.length === 0 ? {} : JSON.parse(rawText);
  } catch {
    throw new HttpError(res.status, rawText, 'response body was not valid JSON');
  }
  const parsed = schema.safeParse(parsedUnknown);
  if (!parsed.success) {
    const issues = parsed.error.issues.map((i) => `${i.path.join('.')}: ${i.message}`).join('; ');
    throw new HttpError(res.status, rawText, `response failed zod schema [${issues}]`);
  }
  return { status: res.status, ok: res.ok, body: parsed.data, rawText };
}

/** Variant for endpoints whose failure bodies matter as much as success. */
export async function requestLoose(
  opts: RequestOptions,
): Promise<{ status: number; body: unknown; rawText: string }> {
  const res = await fetchWithConnectionRetry(opts);
  const rawText = await res.text();
  const body: unknown = rawText.length === 0 ? null : JSON.parse(rawText);
  return { status: res.status, body, rawText };
}

export function authHeaders(token: string): Record<string, string> {
  return { Authorization: `Bearer ${token}`, 'Content-Type': 'application/json' };
}

let idempotencyCounter = 0;

/** Client-generated keys must be unique per logical request (openapi header rule). */
export function newIdempotencyKey(label: string): string {
  idempotencyCounter += 1;
  return `sauron-test-${label}-${Date.now()}-${idempotencyCounter}`;
}
