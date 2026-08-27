// Static-render contract for the /candidates/[id] client shell (B2 SSR fix).
// Pin-mismatch semantics (?at= pin state, frozen ruling #2) live in the pure
// CandidateDetailView so they stay testable without a DOM: a pin that does
// not match the served decision renders the honest notice, never a fake
// "snapshot".
import { describe, expect, it, vi } from 'vitest';
import { renderToStaticMarkup } from 'react-dom/server';
import { CandidateDetailClient, CandidateDetailView } from './candidate-detail-client';
import type { Candidate, EvidenceDossier } from '@/lib/api-schemas';

// Static renderer has no App Router scope; provide a neutral URL so the
// wrapper renders its loading phase and permalink parsing still executes.
vi.mock('next/navigation', () => ({
  useSearchParams: (): URLSearchParams => new URLSearchParams(''),
}));

const CAND_ID = 'cand_01J8ZC7XK9Q2W3E4R5T6Y70002';
const DEC_ID = 'dec_01J8ZC7XK9Q2W3E4R5T6Y70003';
const OTHER_DEC_ID = 'dec_01J8ZC7XK9Q2W3E4R5T6Y70009';
const INTENT_ID = 'int_01J8ZC7XK9Q2W3E4R5T6Y70001';

const candidate = (overrides: Partial<Candidate> = {}): Candidate => ({
  candidate_id: CAND_ID,
  intent_id: INTENT_ID,
  state: 'validating',
  head_sha: 'abcdef1234567890abcdef1234567890abcdef12',
  cluster_id: null,
  relation_to_rep: null,
  queue_position: 2,
  est_cost_millicents: 250_000,
  ...overrides,
});

const dossier = (decisionId: string): EvidenceDossier => ({
  candidate_id: CAND_ID,
  intent_id: INTENT_ID,
  generated_at: '2026-08-23T03:41:00Z',
  inputs_hash: 'sha256:9f1c',
  decision: {
    decision_id: decisionId,
    verb: 'eligible_for_merge_train',
    confidence: 0.94,
    policy: { policy_id: 'pol_payments_high_risk', version: 4 },
    summary: 'Eligible for merge train; canary required.',
  },
  evidence_accepted: [],
  evidence_deferred: [],
  known_uncertainty: [],
  required_post_merge: [],
});

describe('CandidateDetailClient (loading phase)', () => {
  it('renders skeleton-only markup before the gateway responds', () => {
    const html = renderToStaticMarkup(<CandidateDetailClient candidateId={CAND_ID} />);
    expect(html).toContain('data-testid="skeleton"');
    expect(html).not.toContain('Eligible for merge train');
  });
});

describe('CandidateDetailView', () => {
  it('renders header facts plus dossier sections for the happy path', () => {
    const html = renderToStaticMarkup(
      <CandidateDetailView
        candidate={candidate()}
        dossier={dossier(DEC_ID)}
        dossierError={null}
        pinnedDecisionId={null}
        fromGithubCheck={false}
      />,
    );
    expect(html).toContain(CAND_ID.slice(0, 14));
    expect(html).toContain(`href="/intents/${INTENT_ID}"`);
    expect(html).toContain('queue position 2');
    expect(html).toContain('2.50 USD'); // 250000/100000
    expect(html).toContain('Eligible for merge train; canary required.');
    // Share row copy is the permalink trust contract.
    expect(html).toContain('permalink is safe for PRs &amp; slack');
  });

  it('renders the gh_check context chip only when ?src=gh_check arrives', () => {
    const chipless = renderToStaticMarkup(
      <CandidateDetailView
        candidate={candidate()}
        dossier={null}
        dossierError={null}
        pinnedDecisionId={null}
        fromGithubCheck={false}
      />,
    );
    expect(chipless).not.toContain('via github check');
    const chipped = renderToStaticMarkup(
      <CandidateDetailView
        candidate={candidate()}
        dossier={null}
        dossierError={null}
        pinnedDecisionId={null}
        fromGithubCheck={true}
      />,
    );
    expect(chipped).toContain('via github check');
  });

  it('shows the pin-unavailable notice when ?at= targets another decision', () => {
    const html = renderToStaticMarkup(
      <CandidateDetailView
        candidate={candidate()}
        dossier={dossier(DEC_ID)}
        dossierError={null}
        pinnedDecisionId={OTHER_DEC_ID}
        fromGithubCheck={false}
      />,
    );
    expect(html).toContain('data-testid="pin-unavailable"');
    expect(html).toContain('decision snapshot unavailable — showing latest');
    expect(html).not.toContain('data-testid="pin-active"');
  });

  it('confirms an exact ?at= match as actively pinned', () => {
    const html = renderToStaticMarkup(
      <CandidateDetailView
        candidate={candidate()}
        dossier={dossier(DEC_ID)}
        dossierError={null}
        pinnedDecisionId={DEC_ID}
        fromGithubCheck={false}
      />,
    );
    expect(html).toContain('data-testid="pin-active"');
    expect(html).not.toContain('data-testid="pin-unavailable"');
  });

  it('surfaces a failed dossier fetch through the honest error slot', () => {
    const html = renderToStaticMarkup(
      <CandidateDetailView
        candidate={candidate()}
        dossier={null}
        dossierError={{ code: 'schema_mismatch', message: 'response violated the v1 contract schema' }}
        pinnedDecisionId={null}
        fromGithubCheck={false}
      />,
    );
    expect(html).toContain('data-testid="error-state"');
    expect(html).toContain('schema_mismatch');
  });
});
