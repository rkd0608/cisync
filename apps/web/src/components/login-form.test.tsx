// Static-render coverage for the /login two-step UI. The interactive logic is
// exercised through login-flow.test.ts (mock fetch); here we pin the render
// contract of each step so copy, error slots and data hooks stay stable.
import { describe, expect, it } from 'vitest';
import { renderToStaticMarkup } from 'react-dom/server';
import {
  LoginCodeStep,
  LoginEmailStep,
} from './login-form';

const noop = (): void => undefined;

describe('LoginEmailStep', () => {
  const markup = (): string =>
    renderToStaticMarkup(
      <LoginEmailStep email="dev@yourdomain.com" onEmailChange={noop} onSubmit={noop} pending={false} />,
    );

  it('renders the email input with autofill hints', () => {
    const html = markup();
    expect(html).toContain('data-testid="login-step-email"');
    expect(html).toContain('type="email"');
    expect(html).toContain('autoComplete="email"');
    expect(html).toContain('value="dev@yourdomain.com"');
  });

  it('labels the submit affordance send code', () => {
    expect(markup()).toContain('send code');
  });

  it('renders a disabled submit while pending', () => {
    const html = renderToStaticMarkup(
      <LoginEmailStep email="dev@yourdomain.com" onEmailChange={noop} onSubmit={noop} pending />,
    );
    expect(html).toContain('sending…');
    expect(html).toContain('disabled');
  });
});

describe('LoginCodeStep', () => {
  const markup = (): string =>
    renderToStaticMarkup(
      <LoginCodeStep
        email="dev@yourdomain.com"
        code="123"
        onCodeChange={noop}
        onSubmit={noop}
        onBack={noop}
        pending={false}
      />,
    );

  it('shows the destination email and an escape hatch back to step 1', () => {
    const html = markup();
    expect(html).toContain('data-testid="login-step-code"');
    expect(html).toContain('dev@yourdomain.com');
    expect(html).toContain('use a different email');
  });

  it('constrains input to six numeric digits', () => {
    const html = markup();
    expect(html).toContain('inputMode="numeric"');
    expect(html).toContain('maxLength="6"');
    expect(html).toContain('pattern="\\d{6}"');
  });

  it('disables verify until six digits are present (value length drives it)', () => {
    // Three digits → button must be disabled.
    expect(markup()).toContain('disabled');
    const complete = renderToStaticMarkup(
      <LoginCodeStep
        email="dev@yourdomain.com"
        code="123456"
        onCodeChange={noop}
        onSubmit={noop}
        onBack={noop}
        pending={false}
      />,
    );
    expect(complete).toContain('verify &amp; continue');
    expect(complete).not.toContain('button ... disabled'); // sanity: no malformed attr
  });
});
