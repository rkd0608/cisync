// Mutating operations from openapi.yaml. Every mutation carries an
// Idempotency-Key so replays return the original response (invariant I-12).
import { z } from 'zod';
import {
  candidateAcceptedSchema,
  intentGrantSchema,
  leaseRenewalSchema,
} from './api-schemas';
import { request } from './cisync-api';
import { idempotencyKeyHeader, newIdempotencyKey } from './idempotency-key';

const intentCreateSchema = z.object({
  goal: z.string().max(2000),
  repository: z.string(),
  base: z.string(),
  expected_surfaces: z.array(z.string()),
  acceptance_criteria: z.array(z.string()),
  risk: z.enum(['low', 'medium', 'high', 'critical']),
});

const candidateSubmitSchema = z.object({
  patch_ref: z.string(),
  head_sha: z.string().length(40),
  base_sha: z.string().length(40),
});

function post(path: string, body: unknown): RequestInit {
  return {
    method: 'POST',
    headers: {
      'content-type': 'application/json',
      [idempotencyKeyHeader]: newIdempotencyKey(),
    },
    body: JSON.stringify(body),
  };
}

export type IntentCreateInput = z.infer<typeof intentCreateSchema>;
export type CandidateSubmitInput = z.infer<typeof candidateSubmitSchema>;

export function createIntent(input: IntentCreateInput) {
  const check = intentCreateSchema.safeParse(input);
  if (!check.success) {
    return Promise.reject(new TypeError('createIntent: invalid input'));
  }
  return request(intentGrantSchema, '/v1/change-intents', post('', check.data));
}

export function submitCandidate(
  intentId: string,
  input: CandidateSubmitInput,
) {
  const check = candidateSubmitSchema.safeParse(input);
  if (!check.success) {
    return Promise.reject(new TypeError('submitCandidate: invalid input'));
  }
  return request(
    candidateAcceptedSchema,
    `/v1/change-intents/${intentId}/candidates`,
    post('', check.data),
  );
}

export function renewLease(leaseId: string, ttlSeconds?: number) {
  return request(
    leaseRenewalSchema,
    `/v1/leases/${leaseId}/renew`,
    post('', ttlSeconds === undefined ? {} : { ttl_seconds: ttlSeconds }),
  );
}
