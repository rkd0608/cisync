import { InstallationsClient } from '@/components/installations-client';

export const dynamic = 'force-dynamic';

// §2.2 installation/repo status. WHY the page no longer server-fetches (B2
// SSR fix): the relative gateway path has no URL base during SSR, so the
// old pre-render always produced a fabricated "unreachable" error before the
// client could speak. The client shell fetches on mount through the
// same-origin proxy instead — identical semantics to the dashboard board.
export default function InstallationsPage(): React.ReactElement {
  return (
    <div className="flex flex-col gap-6">
      <div>
        <h1 className="font-mono text-lg tracking-wide text-zinc-100">installations</h1>
        <p className="mt-1 max-w-3xl text-sm text-zinc-500">
          Webhook pipe health per repo. A stalled delivery means GitHub events
          stopped flowing — checks will not appear until it resumes.
        </p>
      </div>
      <InstallationsClient />
    </div>
  );
}
