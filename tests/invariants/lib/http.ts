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

export async function request<T>(opts: RequestOptions, schema: ZodType<T>): Promise<HttpResponse<T>> {
  const res = await fetch(opts.url, {
    method: opts.method,
    headers: opts.headers,
    body: opts.body === undefined ? undefined : JSON.stringify(opts.body),
    signal: opts.signal,
  });
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
  const res = await fetch(opts.url, {
    method: opts.method,
    headers: opts.headers,
    body: opts.body === undefined ? undefined : JSON.stringify(opts.body),
  });
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
