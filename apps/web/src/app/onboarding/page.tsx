import { ConnectWizard } from '@/components/connect-wizard';

export const metadata = {
  title: 'Connect Sauron · onboarding',
};

// §2.1 zero-to-first-event wizard. All backend calls happen client-side inside
// the wizard so endpoint absence degrades to honest awaiting-backend states.
export default function OnboardingPage(): React.ReactElement {
  return (
    <div className="flex flex-col gap-6">
      <div>
        <h1 className="font-mono text-lg tracking-wide text-zinc-100">connect sauron</h1>
        <p className="mt-1 max-w-3xl text-sm text-zinc-500">
          Install the GitHub App, prove webhooks flow, then review the compiled-in
          posture. Nothing is enforced during any of this — shadow mode only.
        </p>
      </div>
      <ConnectWizard />
    </div>
  );
}
