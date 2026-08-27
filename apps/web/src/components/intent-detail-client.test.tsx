// Static-render contract for the /intents/[id] client shell (B2 SSR fix:
// data loading moved from the server component to the browser so every fetch
// goes through the same-origin gateway). The wrapper's phases are asserted
// via the skeleton; loaded/not-found/error markup lives in pure subcomponents
// pinned here (repo testing posture: renderToStaticMarkup, no DOM env yet).
import { describe, expect, it } from 'vitest';
import { renderToStaticMarkup } from 'react-dom/server';
import { IntentDetailClient, IntentDetailView } from './intent-detail-client';
import { NotFoundState } from './not-found-state';
import type { Candidate, Intent } from '@/lib/api-schemas';

const INTENT_ID = 'int_01J8ZC7XK9Q2W3E4R5T6Y70001';
const CAND_ID = 'cand_01J8ZC7XK9Q2W3E4R5T6Y70002';

const intent = (overrides: Partial<Intent> = {}): Intent => ({
  intent_id: INTENT_ID,
  state: 'validating',
  goal: 'refactor checkout tax rounding',
  repository: 'acme/checkout',
  owned_surfaces: ['src/tax.ts'],
  risk_class: 'medium',
  evidence_completeness_pct: 0.42,
  created_at: '2026-08-20T10:00:00Z',
  ...overrides,
});

const candidate = (overrides: Partial<Candidate> = {}): Candidate => ({
  candidate_id: CAND_ID,
  state: 'validating',
  head_sha: 'a'.repeat(40),
  cluster_id: null,
  relation_to_rep: null,
  intent_id: INTENT_ID,
  queue_position: null,
  est_cost_millicents: 123_400,
  ...overrides,
});

describe('IntentDetailClient (loading phase)', () => {
  it('renders the shared pulse skeleton and never an error or fabricated data', () => {
    const html = renderToStaticMarkup(<IntentDetailClient intentId={INTENT_ID} />);
    expect(html).toContain('data-testid="skeleton"');
    expect(html).not.toContain('control-plane response rejected');
    expect(html).not.toContain('resource not found');
  });
});

describe('IntentDetailView', () => {
  it('renders envelope facts, id citations and candidate rows for a live intent', () => {
    const html = renderToStaticMarkup(
      <IntentDetailView
        intent={intent({ deadline: null })}
        candidates={[candidate()]}
      />,
    );
    expect(html).toContain('data-state="validating"');
    expect(html).toContain('refactor checkout tax rounding');
    expect(html).toContain('acme/checkout');
    expect(html).toContain(INTENT_ID);
    expect(html).toContain('candidates (1)');
    expect(html).toContain(`href="/candidates/${CAND_ID}"`);
    expect(html).toContain('none'); // deadline absent → honest label
    // Est. spend sums submitted candidates: 123400/100000 = 1.23 USD.
    expect(html).toContain('1.23 USD');
    // A validating intent has NO human action slot — no dead controls.
    expect(html).not.toContain('data-testid="human-action-slot"');
  });

  it('exposes the human action slot ONLY at H-gated blocked state', () => {
    const html = renderToStaticMarkup(
      <IntentDetailView intent={intent({ state: 'blocked' })} candidates={[]} />,
    );
    expect(html).toContain('data-testid="human-action-slot"');
    expect(html).toContain('candidates (0)');
    expect(html).toContain('no candidates');
  });

  it('says WHY the candidate pane is empty when the listing call failed', () => {
    const html = renderToStaticMarkup(
      <IntentDetailView
        intent={intent()}
        candidates={[]}
        candidatesListFailed
      />,
    );
    expect(html).toContain('candidate listing failed; showing nothing rather than partial data');
  });

  it('cites a closed_at timestamp when the terminal intent has one', () => {
    const html = renderToStaticMarkup(
      <IntentDetailView
        intent={intent({ state: 'completed', closed_at: '2026-08-21T09:30:00Z' })}
        candidates={[]}
      />,
    );
    expect(html).toContain('closed 2026-08-21T09:30:00Z');
  });
});

describe('NotFoundState', () => {
  it('keeps the uniform-404 copy (cross-tenant ids stay indistinguishable)', () => {
    const html = renderToStaticMarkup(<NotFoundState />);
    expect(html).toContain('signal lost');
    expect(html).toContain('resource not found');
    expect(html).toContain('uniform 404');
  });
});
