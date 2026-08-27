// Static-render contract for the /clusters/[id] client shell (B2 SSR fix).
// Representative-first ordering is THE reading rule (mission Part 3), so it
// is pinned at the render level: the rep chip must precede every member row.
import { describe, expect, it } from 'vitest';
import { renderToStaticMarkup } from 'react-dom/server';
import { ClusterDetailClient, ClusterDetailView } from './cluster-detail-client';
import type { Cluster } from '@/lib/api-schemas';

const CLUS_ID = 'clus_01J8ZC7XK9Q2W3E4R5T6Y70004';
const REP_ID = 'cand_01J8ZC7XK9Q2W3E4R5T6Y70002';
const MEMBER_ID = 'cand_01J8ZC7XK9Q2W3E4R5T6Y70007';

const cluster = (): Cluster => ({
  cluster_id: CLUS_ID,
  repo: 'acme/checkout',
  rep_candidate_id: REP_ID,
  state: 'active',
  strategy_version: 'v3',
  members: [
    { candidate_id: REP_ID, relation_to_rep: null, similarity_score: 1 },
    { candidate_id: MEMBER_ID, relation_to_rep: 'duplicate_of', similarity_score: 0.61 },
  ],
});

describe('ClusterDetailClient (loading phase)', () => {
  it('renders skeleton-only markup before data arrives', () => {
    const html = renderToStaticMarkup(<ClusterDetailClient clusterId={CLUS_ID} />);
    expect(html).toContain('data-testid="skeleton"');
    expect(html).not.toContain('representative first');
  });
});

describe('ClusterDetailView', () => {
  it('renders the graph, rep-first membership and similarity citations', () => {
    const html = renderToStaticMarkup(<ClusterDetailView cluster={cluster()} />);
    expect(html).toContain('cluster · 2 members');
    expect(html).toContain('strategy v3');
    expect(html).toContain('acme/checkout');
    expect(html).toContain('representative');
    // Rep sorts before every other member regardless of input array order.
    const repAt = html.indexOf(`href="/candidates/${REP_ID}"`);
    const memberAt = html.indexOf(`href="/candidates/${MEMBER_ID}"`);
    expect(repAt).toBeGreaterThanOrEqual(0);
    expect(memberAt).toBeGreaterThan(repAt);
    expect(html).toContain('similarity 61%');
  });

  it('renders the honest empty state when a cluster has no members', () => {
    const html = renderToStaticMarkup(
      <ClusterDetailView
        cluster={{ ...cluster(), members: [], rep_candidate_id: REP_ID }}
      />,
    );
    expect(html).toContain('no members');
    expect(html).toContain('Clustering forms when ≥2 candidates overlap ≥0.6 path-similarity.');
  });

  it('falls back to the single-member whisper when only the rep exists', () => {
    const html = renderToStaticMarkup(
      <ClusterDetailView
        cluster={{
          ...cluster(),
          members: [{ candidate_id: REP_ID, relation_to_rep: null, similarity_score: 1 }],
        }}
      />,
    );
    expect(html).toContain('single-member cluster — no relations to draw');
  });
});
