import { redirect } from 'next/navigation';

// Onboarding collapsed into /app/setup (mission Part 1). This path stays
// alive purely as a redirect so pre-existing links (landing footer, console
// empty states, GitHub App setup notes) keep working.
export default function OnboardingRedirect(): never {
  redirect('/app/setup');
}
