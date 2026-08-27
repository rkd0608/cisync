import { ClusterDetailClient } from '@/components/cluster-detail-client';
import { requireClusterId } from '@/lib/route-guards';

export const dynamic = 'force-dynamic';

// Server boundary shell — param validation only. Data loading rationale:
// cluster-detail-client.tsx header comment (B2 SSR fix).
export default async function ClusterPage({
  params,
}: {
  params: Promise<{ id: string }>;
}): Promise<React.ReactElement> {
  const { id } = await params;
  requireClusterId(id);
  return <ClusterDetailClient clusterId={id} />;
}
