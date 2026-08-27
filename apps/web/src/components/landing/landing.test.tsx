// Static-render tests for the public landing surface (react-dom/server, no
// DOM needed). Verifies the marketing contract: hero copy, both CTAs, the
// three-step explainer, the feature grid, and honest degradation when the
// GitHub App install URL env var is absent.
import { afterAll, beforeAll, describe, expect, it } from 'vitest';
import { renderToStaticMarkup } from 'react-dom/server';
import LandingPage from '@/app/page';

const ORIGINAL_INSTALL_URL = process.env.NEXT_PUBLIC_CISYNC_GITHUB_APP_INSTALL_URL;

describe('landing page', () => {
  beforeAll(() => {
    process.env.NEXT_PUBLIC_CISYNC_GITHUB_APP_INSTALL_URL =
      'https://github.com/apps/cisync-test/installations/new';
  });
  afterAll(() => {
    if (ORIGINAL_INSTALL_URL === undefined) delete process.env.NEXT_PUBLIC_CISYNC_GITHUB_APP_INSTALL_URL;
    else process.env.NEXT_PUBLIC_CISYNC_GITHUB_APP_INSTALL_URL = ORIGINAL_INSTALL_URL;
  });

  const html = (): string => renderToStaticMarkup(<LandingPage />);

  it('renders the hero headline and subline', () => {
    const markup = html();
    expect(markup).toContain('every agent-authored change');
    expect(markup).toContain('verified before merge');
    expect(markup).toContain('Evidence dossiers instead of green checkmarks');
  });

  it('renders both CTAs: Get started → /login, Install → configured app URL', () => {
    const markup = html();
    expect(markup).toContain('data-testid="cta-get-started"');
    expect(markup).toContain('href="/login"');
    expect(markup).toContain('data-testid="cta-install-app"');
    expect(markup).toContain('https://github.com/apps/cisync-test/installations/new');
  });

  it('renders the three-step how-it-works section', () => {
    const markup = html();
    expect(markup).toContain('how it works');
    for (const step of ['Install App', 'Open PR', 'Verified check']) {
      expect(markup).toContain(step);
    }
  });

  it('renders the four-feature grid', () => {
    const markup = html();
    for (const feature of [
      'Evidence dossiers',
      'Priority scheduler',
      'Bounded repair',
      'Tamper-evident ledger',
    ]) {
      expect(markup).toContain(feature);
    }
  });

  it('renders a footer with sign-in and guided-setup links', () => {
    const markup = html();
    expect(markup).toContain('data-testid="landing-footer"');
    expect(markup).toContain('href="/app/setup"');
  });

  it('renders the social-proof strip as explicitly-labeled placeholders (no fabricated logos)', () => {
    const markup = html();
    expect(markup).toContain('data-testid="social-proof-strip"');
    expect(markup).toContain('pilot cohort forming');
    expect(markup).not.toContain('Trusted by'); // false authority would violate T4
  });

  describe('when NEXT_PUBLIC_CISYNC_GITHUB_APP_INSTALL_URL is unset', () => {
    beforeAll(() => {
      delete process.env.NEXT_PUBLIC_CISYNC_GITHUB_APP_INSTALL_URL;
    });

    it('shows an honest not-configured note instead of a fabricated link', () => {
      const markup = renderToStaticMarkup(<LandingPage />);
      expect(markup).toContain('data-testid="cta-install-app-unset"');
      expect(markup).toContain('install URL not configured');
      expect(markup).not.toContain('data-testid="cta-install-app"');
    });
  });
});
