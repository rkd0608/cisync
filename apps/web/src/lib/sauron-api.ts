// Sauron API client. WHY fail-closed: every response is validated against a
// zod schema derived from packages/contracts/openapi.yaml before it reaches a
// component (charter §2 boundary validation); malformed payloads become error
// results instead of rendered data.
import { z } from 'zod';
import {
  candidateIdSchema,
  candidateSchema,
  clusterIdSchema,
  clusterSchema,
  decisionIdSchema,
  errorEnvelopeSchema,
  evidenceDossierSchema,
  intentIdSchema,
  intentSchema,
  type Candidate,
  type Cluster,
  type EvidenceDossier,
  type Intent,
} from './api-schemas';
import {
  installationsStatusResponseSchema,
  type InstallationsStatusResponse,
} from './installation-schemas';
import {
  activePoliciesResponseSchema,
  type ActivePoliciesResponse,
} from './policy-schema';
import { eventsPageSchema, type EventEnvelope } from './event-schemas';

// WHY a relative default: the browser bundle cannot rely on NEXT_PUBLIC_*
// inlining (docker builds bake it before env exists) and direct cross-origin
// calls to control-plane fail without CORS. The Next.js server proxies
// /api/sauron/* → SAURON_API_URL (see next.config.ts), so the client talks to
// its own origin and every deployment works with zero build-time config.
const DEFAULT_BASE_URL = '/api/sauron';

export type ApiFailure = {
  ok: false;
  status: number;
  code: string;
  message: string;
  retryAfterS?: number | null;
};

export type ApiSuccess<T> = { ok: true; data: T };
export type ApiResult<T> = ApiSuccess<T> | ApiFailure;

export function apiBaseUrl(): string {
  return process.env.NEXT_PUBLIC_SAURON_API_URL ?? DEFAULT_BASE_URL;
}

export function isNotFound<T>(result: ApiResult<T>): boolean {
  // Uniform 404: cross-tenant ids are indistinguishable from nonexistent ones.
  return !result.ok && result.code === 'not_found';
}

export function isBadCursor<T>(result: ApiResult<T>): boolean {
  // 416 = after_seq predates ledger retention; callers reset the cursor to 0.
  return !result.ok && result.status === 416;
}

export function apiFailure(
  status: number,
  code: string,
  message: string,
  retryAfterS?: number | null,
): ApiFailure {
  return { ok: false, status, code, message, retryAfterS };
}

async function parseErrorBody(
  response: Response,
): Promise<{ code: string; message: string; retryAfterS?: number | null }> {
  try {
    const body: unknown = await response.json();
    const parsed = errorEnvelopeSchema.safeParse(body);
    if (parsed.success) {
      const envelope = parsed.data.error;
      return {
        code: envelope.code,
        message: envelope.message,
        retryAfterS: envelope.retry_after_s ?? null,
      };
    }
  } catch {
    // Body was not JSON at all; fall through to unparseable_error.
  }
  return {
    code: 'unparseable_error',
    message: `upstream returned HTTP ${response.status} with a non-conforming error body`,
  };
}

export async function request<T>(
  schema: z.ZodType<T>,
  path: string,
  init?: RequestInit,
): Promise<ApiResult<T>> {
  let response: Response;
  try {
    response = await fetch(`${apiBaseUrl()}${path}`, {
      cache: 'no-store',
      ...init,
    });
  } catch (cause) {
    return apiFailure(0, 'network_error', `control-plane unreachable: ${String(cause)}`);
  }

  if (!response.ok) {
    const err = await parseErrorBody(response);
    return apiFailure(response.status, err.code, err.message, err.retryAfterS);
  }

  let body: unknown;
  try {
    body = await response.json();
  } catch {
    return apiFailure(response.status, 'unparseable_error', 'response body was not valid JSON');
  }

  const parsed = schema.safeParse(body);
  if (!parsed.success) {
    return apiFailure(
      response.status,
      'schema_mismatch',
      'response violated the v1 contract schema; refusing to render',
    );
  }
  return { ok: true, data: parsed.data };
}

function guard<T>(
  schema: z.ZodType<string>,
  raw: string,
  op: () => Promise<ApiResult<T>>,
): Promise<ApiResult<T>> {
  if (!schema.safeParse(raw).success) {
    return Promise.resolve(apiFailure(400, 'validation_failed', `malformed id: ${raw}`));
  }
  return op();
}

export function getIntent(intentId: string): Promise<ApiResult<Intent>> {
  return guard(intentIdSchema, intentId, () =>
    request(intentSchema, `/v1/change-intents/${intentId}`),
  );
}

export function listCandidates(intentId: string): Promise<ApiResult<Candidate[]>> {
  return guard(intentIdSchema, intentId, () =>
    request(z.array(candidateSchema), `/v1/change-intents/${intentId}/candidates`),
  );
}

export function getCandidate(candidateId: string): Promise<ApiResult<Candidate>> {
  return guard(candidateIdSchema, candidateId, () =>
    request(candidateSchema, `/v1/candidates/${candidateId}`),
  );
}

// G4 dossier pinning: ?at=dec_… requests the decision block pinned to that
// Decision. Until the backend lands (gap G4) servers ignore the unknown query
// param and return latest — callers render the pin-mismatch notice, so the
// permalink degrades gracefully instead of lying.
export function getDossier(
  candidateId: string,
  at?: string,
): Promise<ApiResult<EvidenceDossier>> {
  if (at !== undefined && !decisionIdSchema.safeParse(at).success) {
    return Promise.resolve(apiFailure(400, 'validation_failed', `malformed decision id: ${at}`));
  }
  const suffix = at === undefined ? '' : `?at=${encodeURIComponent(at)}`;
  return guard(candidateIdSchema, candidateId, () =>
    request(evidenceDossierSchema, `/v1/candidates/${candidateId}/dossier${suffix}`),
  );
}

export function getInstallationsStatus(): Promise<ApiResult<InstallationsStatusResponse>> {
  return request(installationsStatusResponseSchema, '/v1/installations/status');
}

export function getActivePolicies(): Promise<ApiResult<ActivePoliciesResponse>> {
  return request(activePoliciesResponseSchema, '/v1/policies/active');
}

export function getCluster(clusterId: string): Promise<ApiResult<Cluster>> {
  return guard(clusterIdSchema, clusterId, () =>
    request(clusterSchema, `/v1/clusters/${clusterId}`),
  );
}

export interface EventTailQuery {
  afterSeq: number;
  types?: string[];
  aggregate?: string;
  limit?: number;
}

export interface EventTailPage {
  events: EventEnvelope[];
  nextSeq: number;
}

export async function getEvents(query: EventTailQuery): Promise<ApiResult<EventTailPage>> {
  const params = new URLSearchParams();
  params.set('after_seq', String(Math.max(0, Math.floor(query.afterSeq))));
  if (query.types && query.types.length > 0) params.set('types', query.types.join(','));
  if (query.aggregate) params.set('aggregate', query.aggregate);
  if (query.limit !== undefined) params.set('limit', String(query.limit));
  const result = await request(eventsPageSchema, `/v1/events?${params.toString()}`);
  if (!result.ok) return result;
  return { ok: true, data: { events: result.data.events, nextSeq: result.data.next_seq } };
}
