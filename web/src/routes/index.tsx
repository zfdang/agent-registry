import { createRoute, Link, useNavigate } from '@tanstack/react-router'
import { useQuery } from '@tanstack/react-query'
import { rootRoute } from './root'
import { api } from '../api/client'
import { queryKeys } from '../api/query-client'
import { StatsBar } from '../components/stats-bar'
import { SearchBox } from '../components/search-box'
import { AgentCard } from '../components/agent-card'
import { Pager } from '../components/pager'
import { Skeleton } from '../components/ui/skeleton'
import { ErrorBanner } from '../components/ui/error-banner'
import { buttonClasses } from '../components/ui/button'

// 18 fills the browse grid evenly in both layouts (3-col desktop → 6 rows,
// 2-col tablet → 9 rows); the API default of 20 leaves a ragged last row.
const PAGE_SIZE = 18

type IndexSearch = {
  q?: string
  page?: number
  status?: 'verified' | 'stale'
}

export const indexRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/',
  validateSearch: (search: Record<string, unknown>): IndexSearch => ({
    q: typeof search.q === 'string' && search.q.length > 0 ? search.q : undefined,
    page: Number(search.page) > 0 ? Number(search.page) : 1,
    status:
      search.status === 'verified' || search.status === 'stale' ? search.status : undefined,
  }),
  component: IndexPage,
})

function IndexPage() {
  const { q, page = 1, status } = indexRoute.useSearch()
  const navigate = useNavigate({ from: indexRoute.fullPath })

  const { data, isPending, isError, refetch } = useQuery({
    queryKey: queryKeys.agents({ q, page, page_size: PAGE_SIZE, status }),
    queryFn: () => api.listAgents({ q, page, page_size: PAGE_SIZE, status }),
    placeholderData: (prev) => prev,
  })

  return (
    <div className="flex flex-col gap-8">
      <div className="text-center">
        <h1 className="text-3xl font-semibold text-slate-900 dark:text-slate-100 sm:text-4xl">
          Discover verified AI agents
        </h1>
        <p className="mt-2 text-slate-500 dark:text-slate-400">
          Search the registry by name, skill, owner, URL, or card content.
        </p>
      </div>

      <StatsBar />

      <SearchBox
        value={q ?? ''}
        onChange={(newQ) => navigate({ search: (prev) => ({ ...prev, q: newQ || undefined, page: 1 }) })}
        status={status}
        onStatusChange={(newStatus) =>
          navigate({ search: (prev) => ({ ...prev, status: newStatus, page: 1 }) })
        }
      />

      {isError && <ErrorBanner message="Couldn't load agents." onRetry={() => refetch()} />}

      {isPending && (
        <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-3">
          {Array.from({ length: 6 }).map((_, i) => (
            <Skeleton key={i} className="h-48" />
          ))}
        </div>
      )}

      {!isPending && data && data.agents.length === 0 && (
        <div className="rounded-xl border border-dashed border-slate-300 py-16 text-center dark:border-slate-700">
          <p className="text-slate-500 dark:text-slate-400">No agents match — register yours?</p>
          <Link to="/add" className={`${buttonClasses('primary', 'md')} mt-4 inline-flex`}>
            Add your agent
          </Link>
        </div>
      )}

      {!isPending && data && data.agents.length > 0 && (
        <>
          <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-3">
            {data.agents.map((agent) => (
              <AgentCard key={agent.id} agent={agent} />
            ))}
          </div>
          <Pager
            page={data.page}
            pageSize={data.page_size}
            total={data.total}
            onPageChange={(newPage) => navigate({ search: (prev) => ({ ...prev, page: newPage }) })}
          />
        </>
      )}
    </div>
  )
}
