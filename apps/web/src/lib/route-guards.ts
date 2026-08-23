import { notFound } from 'next/navigation';
import { candidateIdSchema, clusterIdSchema, intentIdSchema } from './api-schemas';

// Route params are an input boundary (charter §2): malformed ids never reach
// the API layer; they render the uniform not-found view instead.
export function requireIntentId(raw: string): string {
  if (!intentIdSchema.safeParse(raw).success) notFound();
  return raw;
}

export function requireCandidateId(raw: string): string {
  if (!candidateIdSchema.safeParse(raw).success) notFound();
  return raw;
}

export function requireClusterId(raw: string): string {
  if (!clusterIdSchema.safeParse(raw).success) notFound();
  return raw;
}
