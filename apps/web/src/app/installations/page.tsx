import { InstallationsClient } from '@/components/installations-client';
import { getInstallationsStatus } from '@/lib/sauron-api';

export const dynamic = 'force-dynamic';

// §2.2 installation/repo status. Server fetch first; endpoint absence renders
// the honest error state (backend G3 lands in parallel) — never a crash.
export default async function InstallationsPage(): Promise<React.ReactElement> {
  const result = await getInstallationsStatus();
  return (
    <div className="flex flex-col gap-6">
      <div>
        <h1 className="font-mono text-lg tracking-wide text-zinc-100">installations</h1>
        <p className="mt-1 max-w-3xl text-sm text-zinc-500">
          Webhook pipe health per repo. A stalled delivery means GitHub events
          stopped flowing — checks will not appear until it resumes.
        </p>
      </div>
      <InstallationsClient
        initialData={result.ok ? result.data : null}
        initialError={
          result.ok ? null : { code: result.code, message: result.message }
        }
      />
    </div>
  );
}
