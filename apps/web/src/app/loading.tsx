import { Skeleton } from '@/components/skeleton';

export default function Loading(): React.ReactElement {
  return (
    <div aria-hidden className="flex flex-col gap-4">
      <Skeleton className="h-8 w-72" rounded="rounded-md" />
      <Skeleton className="h-24" />
      <Skeleton className="h-40" />
    </div>
  );
}
