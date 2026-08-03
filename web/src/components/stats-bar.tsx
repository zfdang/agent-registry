import { useQuery } from '@tanstack/react-query'
import { api } from '../api/client'
import { queryKeys } from '../api/query-client'
import { Card } from './ui/card'
import { Skeleton } from './ui/skeleton'
import { ErrorBanner } from './ui/error-banner'

const TILES: { key: keyof Awaited<ReturnType<typeof api.getStats>>; label: string }[] = [
  { key: 'total_agents', label: 'Agents' },
  { key: 'verified_agents', label: 'Verified' },
  { key: 'new_this_week', label: 'New this week' },
  { key: 'distinct_owners', label: 'Owners' },
]

export function StatsBar() {
  const { data, isPending, isError, refetch } = useQuery({
    queryKey: queryKeys.stats,
    queryFn: api.getStats,
    staleTime: 60_000,
  })

  if (isError) {
    return <ErrorBanner message="Couldn't load stats." onRetry={() => refetch()} />
  }

  return (
    <div className="grid grid-cols-2 gap-4 sm:grid-cols-4">
      {TILES.map((tile) => (
        <Card key={tile.key} className="px-4 py-5 text-center">
          {isPending ? (
            <Skeleton className="mx-auto h-8 w-16" />
          ) : (
            <p className="text-2xl font-semibold text-slate-900 dark:text-slate-100">
              {data![tile.key].toLocaleString()}
            </p>
          )}
          <p className="mt-1 text-sm text-slate-500 dark:text-slate-400">{tile.label}</p>
        </Card>
      ))}
    </div>
  )
}
