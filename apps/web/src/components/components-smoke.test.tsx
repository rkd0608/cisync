// Component smoke tests via react-dom/server (an APPROVED dependency) — no
// jsdom/@testing-library yet (proposed, awaiting approval). These verify the
// render tree contract: honest empty/error sections, badges, dossier §7 layout.
import { describe, expect, it } from 'vitest';
import { renderToStaticMarkup } from 'react-dom/server';
import { ConflictPanel } from './conflict-panel';
import { DossierView } from './dossier-view';
import { EmptyState } from './empty-state';
import { ErrorState } from './error-state';
import { EventTimeline } from './event-timeline';
import { EvidenceBar } from './evidence-bar';
import { RelationBadge } from './relation-badge';
import { RiskPill } from './risk-pill';
import { StateBadge } from './state-badge';
import { StateMachineProgress } from './state-machine-progress';
import type { EventEnvelope } from '@/lib/event-schemas';
import type { EvidenceDossier } from '@/lib/api-schemas';

const dossier: EvidenceDossier = {
  candidate_id: 'cand_01J8ZC7XK9Q2W3E4R5T6Y70002',
  intent_id: 'int_01J8ZC7XK9Q2W3E4R5T6Y70001',
  generated_at: '2026-08-23T03:41:00Z',
  inputs_hash: 'sha256:9f1c',
  decision: {
    decision_id: 'dec_01J8ZC7XK9Q2W3E4R5T6Y70003',
    verb: 'eligible_for_merge_train',
    confidence: 0.94,
    policy: { policy_id: 'pol_payments_high_risk', version: 4 },
    summary: 'Eligible for merge train; browser E2E deferred to canary.',
  },
  evidence_accepted: [
    {
      ev_id: 'ev_4490',
      kind: 'selected_unit',
      verdict: 'pass',
      meta: { selected: 44, skipped_as_non_evidence: 1842 },
    },
  ],
  evidence_deferred: [],
  known_uncertainty: [{ description: 'sandbox gap', mitigation: 'canary' }],
  required_post_merge: [{ kind: 'canary', params: { traffic_pct: 2 } }],
};

describe('badge components', () => {
  it('renders every intent state and degrades unknown states', () => {
    const html = renderToStaticMarkup(
      <div>
        <StateBadge state="merge_ready" />
        <StateBadge state="totally_unknown" />
      </div>,
    );
    expect(html).toContain('data-state="merge_ready"');
    expect(html).toContain('merge ready');
    expect(html).toContain('totally unknown');
  });

  it('renders risk pills with the risk as data attribute', () => {
    const html = renderToStaticMarkup(<RiskPill risk="critical" />);
    expect(html).toContain('data-risk="critical"');
  });

  it('renders an em-dash cell for absent relations', () => {
    const html = renderToStaticMarkup(<RelationBadge relation={null} />);
    expect(html).toContain('--');
  });
});

describe('EvidenceBar', () => {
  it('renders clamped percentages from 0..1 input', () => {
    const html = renderToStaticMarkup(
      <div>
        <EvidenceBar pct={0.42} />
        <EvidenceBar pct={7} />
        <EvidenceBar pct={Number.NaN} />
      </div>,
    );
    expect(html).toContain('42%');
    expect(html).toContain('100%');
    expect(html).toContain('width:0%');
    expect(html).not.toContain('700%');
  });
});

describe('StateMachineProgress', () => {
  it('marks the current step and surfaces rejected terminally', () => {
    const active = renderToStaticMarkup(<StateMachineProgress state="validating" />);
    expect(active).toContain('data-step="validating" data-current');
    const rejected = renderToStaticMarkup(<StateMachineProgress state="rejected" />);
    expect(rejected).toContain('data-step="rejected" data-current');
  });

  it('shows the blocked <-> repairing loop marker', () => {
    const html = renderToStaticMarkup(<StateMachineProgress state="repairing" />);
    expect(html).toContain('&lt;-&gt;');
  });
});

describe('honest state components', () => {
  it('EmptyState renders the three-line teaching contract (§2.8)', () => {
    const html = renderToStaticMarkup(
      <EmptyState
        what="no change activity yet"
        whyEmpty="Open a PR on a connected repo, or POST /v1/change-intents."
        action={{ label: 'connect a repo at /onboarding', href: '/onboarding' }}
      />,
    );
    expect(html).toContain('no change activity yet');
    expect(html).toContain('Open a PR on a connected repo');
    expect(html).toContain('href="/onboarding"');
    expect(html).toContain('connect a repo at /onboarding');
  });

  it('EmptyState keeps why/action optional but what mandatory', () => {
    const bare = renderToStaticMarkup(<EmptyState what="no candidates in view" />);
    expect(bare).toContain('no candidates in view');
    expect(bare).not.toContain('href=');
  });

  it('ErrorState shows machine-readable code; retry button only when wired', () => {
    const plain = renderToStaticMarkup(
      <ErrorState error={{ code: 'schema_mismatch', message: 'bad body' }} />,
    );
    expect(plain).toContain('schema_mismatch');
    expect(plain).not.toContain('<button');

    const retrying = renderToStaticMarkup(
      // eslint-disable-next-line react/no-children-prop
      <ErrorState error={{ code: 'network_error', message: 'unreachable' }} onRetry={() => undefined} />,
    );
    expect(retrying).toContain('<button');
  });

  it('ConflictPanel explains the v1 contract gap when no conflicts exist', () => {
    const html = renderToStaticMarkup(<ConflictPanel conflicts={[]} />);
    expect(html).toContain('does not carry the conflicts[] field');
  });
});

describe('EventTimeline', () => {
  const event: EventEnvelope = {
    id: 'evt_01J8ZC7XK9Q2W3E4R5T6Y70006',
    seq: 1,
    type: 'intent.declared',
    version: 1,
    tenant_id: 'org_test',
    aggregate: { type: 'intent', id: 'int_01J8ZC7XK9Q2W3E4R5T6Y70001' },
    causation_id: 'evt_root',
    correlation_id: 'corr_1',
    actor: { kind: 'agent', id: 'agent:x' },
    occurred_at: '2026-08-23T03:00:05Z',
    payload_sha256: 'aa',
    prev_hash: 'bb',
    entry_hash: 'cc',
    payload: {},
  };

  it('lists entries and renders a quiet placeholder when empty', () => {
    const populated = renderToStaticMarkup(<EventTimeline events={[event]} />);
    expect(populated).toContain('intent.declared');
    expect(populated).toContain('03:00:05');
    const quiet = renderToStaticMarkup(<EventTimeline events={[]} />);
    expect(quiet).toContain('ledger quiet');
  });
});

describe('DossierView (§7 structure)', () => {
  it('renders calibrated verb + confidence + policy version banner', () => {
    const html = renderToStaticMarkup(<DossierView dossier={dossier} />);
    // T4: ledger verb verbatim, word + number side by side.
    expect(html).toContain('data-verb="eligible_for_merge_train"');
    expect(html).toContain('Eligible for merge train');
    expect(html).toContain('moderate · 94.0% confidence');
    expect(html).toContain('pol_payments_high_risk v4');
  });

  it('always renders mandatory deferred section, even when empty (§2.8 copy)', () => {
    const html = renderToStaticMarkup(<DossierView dossier={dossier} />);
    expect(html).toContain('deferred evidence — with reasons (0)');
    expect(html).toContain(
      'Nothing deferred — plan ran everything required by pol_payments_high_risk v4.',
    );
  });

  it('surfaces evidence meta values', () => {
    const html = renderToStaticMarkup(<DossierView dossier={dossier} />);
    expect(html).toContain('selected=44');
    expect(html).toContain('skipped_as_non_evidence=1842');
  });

  it('renders uncertainty and post-merge params', () => {
    const html = renderToStaticMarkup(<DossierView dossier={dossier} />);
    expect(html).toContain('known uncertainty (1)');
    expect(html).toContain('traffic_pct=2');
  });
});
