// Static-render coverage for the /login single-step email+password UI. The
// network logic is exercised through auth-flow.test.ts (mocked fetch); here we
// pin the render contract per signup mode and error slot so copy and data
// hooks stay stable (email+password replaced OTP per SPEC §3 2026-08-26).
import { describe, expect, it } from 'vitest';
import { renderToStaticMarkup } from 'react-dom/server';
import { CredentialsFormView } from './login-form';
import type { AuthStepError } from '@/lib/auth-flow';

const noop = (): void => undefined;

const base = {
  isSignup: false,
  onToggleSignup: noop,
  email: 'dev@yourdomain.com',
  onEmailChange: noop,
  password: '',
  onPasswordChange: noop,
  onSubmit: noop,
  pending: false,
  error: null,
};

describe('CredentialsFormView render contract', () => {
  it('renders a password field with autofill hints for sign-in', () => {
    const html = renderToStaticMarkup(<CredentialsFormView {...base} mode="open" />);
    expect(html).toContain('data-testid="login-form"');
    expect(html).toContain('type="password"');
    expect(html).toContain('autoComplete="current-password"');
    expect(html).toContain('autoComplete="email"');
  });

  it('switches autofill + copy for signup mode', () => {
    const html = renderToStaticMarkup(<CredentialsFormView {...base} mode="open" isSignup />);
    expect(html).toContain('autoComplete="new-password"');
    expect(html).toContain('create your CISync account');
  });

  it('open mode exposes the signup toggle', () => {
    const html = renderToStaticMarkup(<CredentialsFormView {...base} mode="open" />);
    expect(html).toContain('data-testid="signup-toggle"');
    expect(html).toContain('create one');
  });

  it('invite/closed modes hide the toggle and say so honestly', () => {
    for (const mode of ['invite', 'closed'] as const) {
      const html = renderToStaticMarkup(<CredentialsFormView {...base} mode={mode} />);
      expect(html).not.toContain('data-testid="signup-toggle"');
      expect(html).toContain('data-testid="closed-copy"');
      expect(html).toContain('invite-only');
    }
  });

  it('submit is disabled while fields are empty, enabled with valid input, pending swaps copy', () => {
    // WHY lookahead: Tailwind's "disabled:*" utility classes contain the
    // literal token, so match a standalone HTML attribute only.
    const hasDisabledAttr = (html: string): boolean => /data-testid="auth-submit"[^>]*\bdisabled(?=[\s=>])/.test(html);
    expect(hasDisabledAttr(markupWith({}, {}))).toBe(true);
    expect(hasDisabledAttr(markupWith({ password: 'long-enough-pass' }, {}))).toBe(false);
    // Pending disables again and shows the working copy.
    const busy = markupWith({ password: 'long-enough-pass' }, { pending: true });
    expect(busy).toContain('working…');
    expect(hasDisabledAttr(busy)).toBe(true);
  });

  it.each([
    ['invalid_credentials', 'Invalid email or password.'],
    ['weak_password', 'Password must be at least 10 characters.'],
    ['exists', 'An account with this email already exists — sign in instead.'],
    ['rate_limited', 'Too many attempts. Wait a moment, then retry. (12s)'],
  ] as const)('%s error renders its exact copy (uniform message)', (kind, expected) => {
    const error: AuthStepError = kind === 'rate_limited'
      ? { kind, message: '', retryAfterS: 12 }
      : { kind, message: '' };
    expect(renderToStaticMarkup(<CredentialsFormView {...base} mode="open" error={error} />)).toContain(expected);
  });
});

function markupWith(
  fields: Partial<Pick<typeof base, 'email' | 'password'>>,
  state: Partial<Pick<typeof base, 'pending'>>,
): string {
  return renderToStaticMarkup(
    <CredentialsFormView {...base} {...fields} {...state} mode="open" />,
  );
}
