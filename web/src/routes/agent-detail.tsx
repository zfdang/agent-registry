import { createRoute, Link } from '@tanstack/react-router'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { ExternalLink, RefreshCw } from 'lucide-react'
import { rootRoute } from './root'
import { api, ApiError } from '../api/client'
import { queryKeys } from '../api/query-client'
import type { Agent, AgentCapabilities, CardSnapshot } from '../api/types'
import { formatDate, formatDateTime } from '../lib/format-date'
import { toast } from '../lib/toast'
import { VerificationBadge } from '../components/verification/status-badge'
import { CheckList } from '../components/verification/check-list'
import { CopyButton } from '../components/ui/copy-button'
import { JsonViewer } from '../components/json-viewer'
import { SkillList } from '../components/skill-list'
import { Card } from '../components/ui/card'
import { Badge } from '../components/ui/badge'
import { Button } from '../components/ui/button'
import { Skeleton } from '../components/ui/skeleton'
import { ErrorBanner } from '../components/ui/error-banner'
import { agentDetailHref } from '../lib/agent-ref'

// The card's capabilities object → display chips: enabled boolean flags by
// name, plus each declared extension's URI (scheme stripped).
function capabilityChips(caps: AgentCapabilities | null | undefined): string[] {
  if (!caps) return []
  const flags = Object.entries(caps)
    .filter(([, value]) => value === true)
    .map(([key]) => key)
  const extensions = Array.isArray(caps.extensions)
    ? caps.extensions.map((ext) => ext.uri.replace(/^https?:\/\//, ''))
    : []
  return [...flags, ...extensions]
}

export const agentDetailRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/agents/$',
  component: AgentDetailPage,
})

function AgentDetailPage() {
  const { _splat } = agentDetailRoute.useParams()
  const ref = _splat ?? ''
  const queryClient = useQueryClient()

  const agentQuery = useQuery({
    queryKey: queryKeys.agent(ref),
    queryFn: () => api.getAgent(ref),
    retry: false,
  })

  const verificationQuery = useQuery({
    queryKey: queryKeys.verification(ref),
    queryFn: () => api.getVerification(ref),
    enabled: agentQuery.isSuccess,
  })

  const cardsQuery = useQuery({
    queryKey: queryKeys.cards(ref),
    queryFn: () => api.getCardHistory(ref),
    enabled: agentQuery.isSuccess,
  })

  const refreshMutation = useMutation({
    mutationFn: () => api.refresh(ref),
    onSuccess: (res) => {
      const prevCardJson = JSON.stringify(agentQuery.data?.card_json ?? null)
      const nextCardJson = JSON.stringify(res.agent.card_json ?? null)
      queryClient.setQueryData(queryKeys.agent(ref), res.agent)
      queryClient.invalidateQueries({ queryKey: queryKeys.verification(ref) })
      queryClient.invalidateQueries({ queryKey: queryKeys.cards(ref) })
      if (prevCardJson !== nextCardJson) {
        toast.success('agent card updated')
      } else {
        toast.success('verification refreshed')
      }
    },
    onError: (err) => {
      if (err instanceof ApiError && err.status === 429) {
        toast.error('try again in a minute')
      } else {
        toast.error(err instanceof Error ? err.message : 'refresh failed')
      }
    },
  })

  if (agentQuery.isPending) {
    return (
      <div className="flex flex-col gap-6">
        <Skeleton className="h-24" />
        <Skeleton className="h-48" />
        <Skeleton className="h-64" />
      </div>
    )
  }

  if (agentQuery.isError) {
    const err = agentQuery.error
    if (err instanceof ApiError && err.code === 'ambiguous_ref' && err.candidates) {
      return (
        <div className="mx-auto max-w-xl">
          <h1 className="text-xl font-semibold text-slate-900 dark:text-slate-100">Multiple agents match</h1>
          <p className="mt-2 text-sm text-slate-500 dark:text-slate-400">
            This agent ID isn't unique. Pick the agent you meant:
          </p>
          <ul className="mt-4 flex flex-col gap-2">
            {err.candidates.map((c) => (
              <li key={c.id}>
                <Link
                  to={agentDetailHref(c.url)}
                  className="block rounded-lg border border-slate-200 px-4 py-3 hover:bg-slate-50 dark:border-slate-800 dark:hover:bg-slate-900"
                >
                  <p className="font-medium text-slate-900 dark:text-slate-100">{c.name}</p>
                  <p className="text-sm text-slate-500 dark:text-slate-400">{c.url}</p>
                </Link>
              </li>
            ))}
          </ul>
        </div>
      )
    }

    if (err instanceof ApiError && err.code === 'not_found') {
      return (
        <div className="mx-auto max-w-xl text-center">
          <h1 className="text-xl font-semibold text-slate-900 dark:text-slate-100">Agent not found</h1>
          <p className="mt-2 text-sm text-slate-500 dark:text-slate-400">
            No agent matches "{ref}".
          </p>
        </div>
      )
    }

    return <ErrorBanner message="Couldn't load this agent." onRetry={() => agentQuery.refetch()} />
  }

  const agent = agentQuery.data as Agent

  return (
    <div className="flex flex-col gap-8">
      <section className="flex flex-col gap-2">
        <div className="flex flex-wrap items-center gap-3">
          <h1 className="text-2xl font-semibold text-slate-900 dark:text-slate-100">{agent.name}</h1>
          <VerificationBadge status={agent.verification_status} />
        </div>
        {agent.owner_name && (
          <p className="text-sm text-slate-500 dark:text-slate-400">by {agent.owner_name}</p>
        )}
        <div className="flex flex-wrap items-center gap-4 text-sm text-slate-500 dark:text-slate-400">
          <span>registered {formatDate(agent.created_at)}</span>
          {agent.verified_at && <span>verified {formatDate(agent.verified_at)}</span>}
          <a
            href={agent.url}
            target="_blank"
            rel="noopener nofollow"
            className="inline-flex items-center gap-1 text-brand-600 hover:underline dark:text-brand-400"
          >
            {agent.url}
            <ExternalLink className="size-3.5" />
          </a>
        </div>
        {agent.description && <p className="text-slate-600 dark:text-slate-300">{agent.description}</p>}
      </section>

      <Card className="p-5">
        <h2 className="mb-3 text-sm font-semibold uppercase tracking-wide text-slate-500 dark:text-slate-400">
          Identity
        </h2>
        <dl className="flex flex-col gap-3 text-sm">
          <div className="flex items-center gap-2">
            <dt className="w-24 shrink-0 text-slate-500 dark:text-slate-400">Agent ID</dt>
            <dd className="flex items-center gap-1 font-mono text-slate-800 dark:text-slate-200">
              {agent.agent_id ?? '—'}
              {agent.agent_id && <CopyButton value={agent.agent_id} />}
            </dd>
          </div>
          <div className="flex items-center gap-2">
            <dt className="w-24 shrink-0 text-slate-500 dark:text-slate-400">Card URL</dt>
            <dd className="break-all font-mono text-slate-800 dark:text-slate-200">
              {agent.card_url ?? '—'}
            </dd>
          </div>
          <div className="flex items-center gap-2">
            <dt className="w-24 shrink-0 text-slate-500 dark:text-slate-400">Version</dt>
            <dd className="text-slate-800 dark:text-slate-200">{agent.version ?? '—'}</dd>
          </div>
          <div className="flex items-center gap-2">
            <dt className="w-24 shrink-0 text-slate-500 dark:text-slate-400">A2A protocol</dt>
            <dd className="text-slate-800 dark:text-slate-200">
              {agent.protocol_version === null ? (
                <span title="card uses the pre-1.0 format (no supportedInterfaces)">A2A 0.x</span>
              ) : (
                `A2A ${agent.protocol_version}`
              )}
            </dd>
          </div>
        </dl>
      </Card>

      <Card className="p-5">
        <h2 className="mb-3 text-sm font-semibold uppercase tracking-wide text-slate-500 dark:text-slate-400">
          Skills &amp; capabilities
        </h2>
        <div className="flex flex-col gap-3">
          {agent.skills.length > 0 && <SkillList skills={agent.skills} />}
          <div className="flex flex-wrap gap-1.5">
            {capabilityChips(agent.capabilities).map((cap) => (
              <Badge key={cap} tone="gray">
                {cap}
              </Badge>
            ))}
          </div>
          {agent.skills.length === 0 && capabilityChips(agent.capabilities).length === 0 && (
            <p className="text-sm text-slate-400 dark:text-slate-500">None declared.</p>
          )}
        </div>
      </Card>

      <Card className="p-5">
        <div className="mb-3 flex items-center justify-between">
          <h2 className="text-sm font-semibold uppercase tracking-wide text-slate-500 dark:text-slate-400">
            Verification
          </h2>
          <Button
            variant="outline"
            size="sm"
            disabled={refreshMutation.isPending}
            onClick={() => refreshMutation.mutate()}
          >
            <RefreshCw className={`size-4 ${refreshMutation.isPending ? 'animate-spin' : ''}`} />
            Refresh
          </Button>
        </div>
        {verificationQuery.isPending && <Skeleton className="h-32" />}
        {verificationQuery.isError && (
          <ErrorBanner message="Couldn't load verification." onRetry={() => verificationQuery.refetch()} />
        )}
        {verificationQuery.data && <CheckList checks={verificationQuery.data.checks} showExpiry />}
      </Card>

      <Card className="p-5">
        <h2 className="mb-3 text-sm font-semibold uppercase tracking-wide text-slate-500 dark:text-slate-400">
          Current agent card
        </h2>
        <JsonViewer data={agent.card_json ?? {}} summary="agent-card.json" />
      </Card>

      <Card className="p-5">
        <h2 className="mb-3 text-sm font-semibold uppercase tracking-wide text-slate-500 dark:text-slate-400">
          Card history
        </h2>
        {cardsQuery.isPending && <Skeleton className="h-24" />}
        {cardsQuery.isError && (
          <ErrorBanner message="Couldn't load card history." onRetry={() => cardsQuery.refetch()} />
        )}
        {cardsQuery.data && <CardHistory snapshots={cardsQuery.data.snapshots} />}
      </Card>
    </div>
  )
}

function CardHistory({ snapshots }: { snapshots: CardSnapshot[] }) {
  if (snapshots.length === 0) {
    return <p className="text-sm text-slate-400 dark:text-slate-500">No snapshots recorded yet.</p>
  }

  const sourceLabel: Record<CardSnapshot['source'], string> = {
    register: 'registered',
    update: 'updated',
    refresh: 'refreshed',
  }

  return (
    <details open={snapshots.length > 1} className="group">
      <summary className="cursor-pointer text-sm font-medium text-slate-600 dark:text-slate-300">
        {snapshots.length} snapshot{snapshots.length === 1 ? '' : 's'}
      </summary>
      <ul className="mt-3 flex flex-col gap-2">
        {snapshots.map((snap) => (
          <li key={snap.id} className="rounded-lg border border-slate-200 px-3 py-2 dark:border-slate-800">
            <div className="flex flex-wrap items-center gap-2 text-sm">
              <Badge tone="gray">{sourceLabel[snap.source]}</Badge>
              <span className="text-slate-500 dark:text-slate-400">{formatDateTime(snap.fetched_at)}</span>
              <span className="font-mono text-xs text-slate-400 dark:text-slate-500">
                {snap.card_hash.slice(0, 16)}…
              </span>
            </div>
            <div className="mt-2">
              <JsonViewer data={snap.card_json} summary="View snapshot" />
            </div>
          </li>
        ))}
      </ul>
    </details>
  )
}
