import { Suspense } from 'react';
import { CandidateDetailClient } from '@/components/candidate-detail-client';
import { requireCandidateId } from '@/lib/route-guards';

export const dynamic = 'force-dynamic';

// Server boundary shell for the trust-artifact flagship. WHY a Suspense
// wrapper: useSearchParams() inside the client shell requires one during
// prerender (same posture as /dashboard). WHY data loads in the client:
// candidate-detail-client.tsx header comment (B2 SSR fix).
export default async function CandidatePage({
  params,
}: {
  params: Promise<{ id: string }>;
}): Promise<React.ReactElement> {
  const { id } = await params;
  requireCandidateId(id);
  return (
    <Suspense fallback={<div aria-hidden className="h-40 animate-pulse rounded-lg border border-white/5 bg-white/[0.03]" />}>
      <CandidateDetailClient candidateId={id} />
    </Suspense>
  );
}
