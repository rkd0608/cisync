import { IntentDetailClient } from '@/components/intent-detail-client';
import { requireIntentId } from '@/lib/route-guards';

export const dynamic = 'force-dynamic';

// Server shell: validates the route param at the boundary (charter §2) and
// renders the auth-gated client cockpit. WHY the data fetch lives in the
// client shell instead of here: see intent-detail-client.tsx header comment
// (SSR has no base for relative gateway fetches; browser keeps deployment
// env at zero and cookie/bearer semantics identical to the board).
export default async function IntentDetailPage({
  params,
}: {
  params: Promise<{ id: string }>;
}): Promise<React.ReactElement> {
  const { id } = await params;
  requireIntentId(id);
  return <IntentDetailClient intentId={id} />;
}
