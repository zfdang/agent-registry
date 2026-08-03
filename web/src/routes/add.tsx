import { useState } from 'react'
import { createRoute, Link } from '@tanstack/react-router'
import { useMutation } from '@tanstack/react-query'
import { CheckCircle2, Info } from 'lucide-react'
import { rootRoute } from './root'
import { api, ApiError } from '../api/client'
import type { ValidationResult, VerificationStatus } from '../api/types'
import { toast } from '../lib/toast'
import { agentDetailHref } from '../lib/agent-ref'
import { Input } from '../components/ui/input'
import { Button } from '../components/ui/button'
import { Card } from '../components/ui/card'
import { Badge } from '../components/ui/badge'
import { CheckList } from '../components/verification/check-list'
import { VerificationBadge } from '../components/verification/status-badge'
import { AgentMetaChips } from '../components/agent-meta-chips'

export const addRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/add',
  component: AddPage,
})

function isValidHttpsUrl(value: string): boolean {
  try {
    const parsed = new URL(value)
    return parsed.protocol === 'https:'
  } catch {
    return false
  }
}

function resultToStatus(result: ValidationResult): VerificationStatus {
  if (result === 'passed') return 'verified'
  if (result === 'passed_with_warnings') return 'verified_with_warnings'
  return 'failed'
}

function AddPage() {
  const [url, setUrl] = useState('')
  const [validatedUrl, setValidatedUrl] = useState<string | null>(null)

  const validateMutation = useMutation({
    mutationFn: (u: string) => api.validate(u),
    onSuccess: () => setValidatedUrl(url),
    onError: (err) => {
      if (err instanceof ApiError && err.status === 429) {
        toast.error('try again in a minute')
      } else {
        toast.error(err instanceof Error ? err.message : 'validation failed')
      }
    },
  })

  const registerMutation = useMutation({
    mutationFn: (validationId: string) => api.register(validationId),
    onError: (err) => {
      if (err instanceof ApiError && err.status === 410) {
        toast.error('validation expired, please re-validate')
        resetToStep1()
      } else if (err instanceof ApiError && err.status === 429) {
        toast.error('try again in a minute')
      } else {
        toast.error(err instanceof Error ? err.message : 'registration failed')
      }
    },
  })

  function resetToStep1() {
    setValidatedUrl(null)
    validateMutation.reset()
    registerMutation.reset()
  }

  function handleUrlChange(next: string) {
    setUrl(next)
    if (validatedUrl !== null && next !== validatedUrl) {
      resetToStep1()
    }
  }

  function handleValidate() {
    if (!isValidHttpsUrl(url)) return
    validateMutation.mutate(url)
  }

  if (registerMutation.isSuccess) {
    const agent = registerMutation.data
    return (
      <div className="mx-auto max-w-lg text-center">
        <CheckCircle2 className="mx-auto size-12 text-emerald-600 dark:text-emerald-400" />
        <h1 className="mt-4 text-2xl font-semibold text-slate-900 dark:text-slate-100">
          {agent.name} is live
        </h1>
        <p className="mt-2 text-slate-500 dark:text-slate-400">
          Your agent has been registered in the Agent Registry.
        </p>
        <Link to={agentDetailHref(agent.url)} className="mt-6 inline-block text-brand-600 hover:underline dark:text-brand-400">
          View your listing →
        </Link>
      </div>
    )
  }

  const validation = validateMutation.data
  const clientUrlInvalid = url.length > 0 && !isValidHttpsUrl(url)

  return (
    <div className="mx-auto flex max-w-2xl flex-col gap-8">
      <div>
        <h1 className="text-2xl font-semibold text-slate-900 dark:text-slate-100">Add your agent</h1>
        <p className="mt-2 text-slate-500 dark:text-slate-400">
          We'll fetch your agent card from{' '}
          <code className="rounded bg-slate-100 px-1 py-0.5 text-xs dark:bg-slate-800">
            /.well-known/agent-card.json
          </code>{' '}
          and list what it publishes — name, owner, skills, and agent ID all come from the card.
        </p>
      </div>

      <Card className="p-5">
        <label htmlFor="agent-url" className="text-sm font-medium text-slate-700 dark:text-slate-300">
          Agent URL
        </label>
        <div className="mt-2 flex flex-col gap-2 sm:flex-row">
          <Input
            id="agent-url"
            type="url"
            placeholder="https://…"
            value={url}
            onChange={(e) => handleUrlChange(e.target.value)}
            className="flex-1"
          />
          <Button
            onClick={handleValidate}
            disabled={!isValidHttpsUrl(url) || validateMutation.isPending}
          >
            Validate
          </Button>
        </div>
        {clientUrlInvalid && (
          <p className="mt-2 text-sm text-red-600 dark:text-red-400">Enter a valid https:// URL.</p>
        )}
      </Card>

      {(validateMutation.isPending || validation) && (
        <Card className="p-5">
          <h2 className="mb-3 text-sm font-semibold uppercase tracking-wide text-slate-500 dark:text-slate-400">
            Checks
          </h2>
          <CheckList checks={validation?.checks} pending={validateMutation.isPending} />
        </Card>
      )}

      {validation && validation.mode === 'update' && (
        <div className="flex items-start gap-3 rounded-lg border border-brand-200 bg-brand-50 px-4 py-3 text-sm text-brand-800 dark:border-brand-900 dark:bg-brand-950 dark:text-brand-300">
          <Info className="mt-0.5 size-4 shrink-0" />
          <p>
            This URL is already registered; registering will update the existing listing.{' '}
            {validation.existing_id && (
              <Link to={agentDetailHref(url)} className="underline">
                View current listing
              </Link>
            )}
          </p>
        </div>
      )}

      {validation?.extracted && validation.result !== 'failed' && (
        <div>
          <h2 className="mb-3 text-sm font-semibold uppercase tracking-wide text-slate-500 dark:text-slate-400">
            Preview
          </h2>
          <Card className="flex flex-col gap-2 p-5">
            <div className="flex items-start justify-between gap-2">
              <h3 className="font-semibold text-slate-900 dark:text-slate-100">{validation.extracted.name}</h3>
              <VerificationBadge status={resultToStatus(validation.result)} />
            </div>
            {validation.extracted.owner_name && (
              <p className="text-sm text-slate-500 dark:text-slate-400">by {validation.extracted.owner_name}</p>
            )}
            <p className="text-sm text-slate-600 dark:text-slate-300">
              {validation.extracted.description || 'No description provided.'}
            </p>
            {validation.extracted.skills.length > 0 && (
              <div className="flex flex-wrap gap-1.5">
                {validation.extracted.skills.slice(0, 3).map((skill) => (
                  <Badge key={skill.id} tone="brand">
                    {skill.name}
                  </Badge>
                ))}
                {validation.extracted.skills.length > 3 && (
                  <Badge tone="gray">+{validation.extracted.skills.length - 3} more</Badge>
                )}
              </div>
            )}
            <div className="pt-1">
              <AgentMetaChips
                version={validation.extracted.version}
                protocolVersion={validation.extracted.protocol_version}
              />
            </div>
          </Card>

          <Button
            className="mt-4 w-full"
            disabled={!validation.registerable || registerMutation.isPending}
            onClick={() => registerMutation.mutate(validation.validation_id)}
          >
            {validation.mode === 'update' ? 'Update agent' : 'Register agent'}
          </Button>
        </div>
      )}
    </div>
  )
}
