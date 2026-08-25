// Render-state tests for the dossier trust surfaces (W5/C1+C2): decision
// banner L0→L1 expander, permalink context chip, pin/supersede/invalidation
// alerts. react-dom/server only — no jsdom (proposed, awaiting approval).
import { describe, expect, it } from 'vitest';
import { renderToStaticMarkup } from 'react-dom/server';
import { DecisionBanner } from './decision-banner';
import { ContextChip } from './context-chip';
import {
  InvalidatedEvidenceNotice,
  PinUnavailableNotice,
  PinnedDecisionChip,
  SupersededBanner,
} from './dossier-alerts';
import type { Decision } from '@/lib/api-schemas';

const baseDecision: Decision = {
  decision_id: 'dec_01J8ZC7XK9Q2W3E4R5T6Y70003',
  verb: 'eligible_for_merge_train',
  confidence: 0.94,
  policy: { policy_id: 'pol_payments_high_risk', version: 4 },
  summary: 'Eligible for merge train; browser E2E deferred to canary.',
};

describe('DecisionBanner (L0→L1 factor expander)', () => {
  it('hides the expander entirely when no factors exist (no dead controls)', () => {
    const html = renderToStaticMarkup(<DecisionBanner decision={baseDecision} />);
    expect(html).not.toContain('why this decision');
    expect(html).not.toContain('<details');
  });

  it('expands to verbatim ledger factors when explanation lands', () => {
    const withFactors: Decision = {
      ...baseDecision,
      explanation: {
        factors: [
          { name: 'selection_confidence', value: 0.987, source: 'learned_stats:v3' },
          { name: 'inputs_fresh_at_render', value: true, source: 'merge_base.advanced@seq10482' },
        ],
      },
    };
    const html = renderToStaticMarkup(<DecisionBanner decision={withFactors} />);
    expect(html).toContain('why this decision · 2 factors');
    expect(html).toContain('selection_confidence');
    expect(html).toContain('=0.987');
    expect(html).toContain('learned_stats:v3');
    // Boolean factor values render truthfully rather than being coerced away.
    expect(html).toContain('=true');
    // L0 stays first: summary text appears before the details element.
    expect(html.indexOf(baseDecision.summary)).toBeLessThan(html.indexOf('<details'));
  });

  it('renders deferred verdicts in amber outline per §7 color semantics', () => {
    const html = renderToStaticMarkup(
      <DecisionBanner decision={{ ...baseDecision, verb: 'deferred' }} />,
    );
    expect(html).toContain('border-amber-500');
    expect(html).toContain('Deferred');
  });
});

describe('permalink context + pin states (§2.5, §3.2)', () => {
  it('renders the gh_check arrival chip', () => {
    const html = renderToStaticMarkup(<ContextChip kind="github_check" />);
    expect(html).toContain('data-context="github_check"');
    expect(html).toContain('via github check');
  });

  it('superseded banner routes readers to the cluster representative', () => {
    const viaCluster = renderToStaticMarkup(
      <SupersededBanner
        clusterId="clus_01J8ZC7XK9Q2W3E4R5T6Y70006"
        intentId="int_01J8ZC7XK9Q2W3E4R5T6Y70001"
      />,
    );
    expect(viaCluster).toContain('⏹ superseded candidate');
    expect(viaCluster).toContain('href="/clusters/clus_01J8ZC7XK9Q2W3E4R5T6Y70006"');
    expect(viaCluster).toContain('cluster representative');

    const viaIntent = renderToStaticMarkup(
      <SupersededBanner clusterId={null} intentId="int_01J8ZC7XK9Q2W3E4R5T6Y70001" />,
    );
    expect(viaIntent).toContain('href="/intents/int_01J8ZC7XK9Q2W3E4R5T6Y70001"');
  });

  it('invalidated-evidence notice renders only when items exist, listing ev_ids + reason', () => {
    expect(renderToStaticMarkup(<InvalidatedEvidenceNotice items={[]} />)).toBe('');
    const html = renderToStaticMarkup(
      <InvalidatedEvidenceNotice
        items={[{ evId: 'ev_4482', reason: 'upstream marked failed after decision' }]}
      />,
    );
    expect(html).toContain('✕ evidence invalidated (1)');
    expect(html).toContain('ev_4482');
    expect(html).toContain('upstream marked failed after decision');
  });

  it('pin states: mismatch shows honest amber notice; active pin shows chip', () => {
    const unavailable = renderToStaticMarkup(<PinUnavailableNotice />);
    expect(unavailable).toContain('decision snapshot unavailable — showing latest');
    const pinned = renderToStaticMarkup(
      <PinnedDecisionChip decisionId="dec_01J8ZC7XK9Q2W3E4R5T6Y70003" />,
    );
    expect(pinned).toContain('pinned · dec_01…0003');
  });
});
