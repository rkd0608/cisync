import { randomUUID } from 'node:crypto';

// Idempotency-Key header contract: minLength 16, maxLength 128 (openapi.yaml).
// Two UUIDs joined stay well inside that window and make accidental collisions
// across concurrent agents negligible.
export const idempotencyKeyHeader = 'Idempotency-Key';

export function newIdempotencyKey(): string {
  return `${randomUUID()}-${randomUUID()}`;
}

export function isValidIdempotencyKey(key: string): boolean {
  return key.length >= 16 && key.length <= 128;
}
