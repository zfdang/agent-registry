import { Link } from '@tanstack/react-router'
import type { Agent } from '../api/types'
import { agentDetailHref } from '../lib/agent-ref'
import { formatDate } from '../lib/format-date'
import { VerificationBadge } from './verification/status-badge'
import { Card } from './ui/card'
import { Badge } from './ui/badge'

const MAX_SKILLS = 3

export function AgentCard({ agent }: { agent: Agent }) {
  const shownSkills = agent.skills.slice(0, MAX_SKILLS)
  const extraCount = agent.skills.length - shownSkills.length

  return (
    <Link to={agentDetailHref(agent.url)} className="block h-full">
      <Card className="flex h-full flex-col gap-2 p-5 transition-shadow hover:shadow-md">
        <div className="flex items-start justify-between gap-2">
          <h3 className="font-semibold text-slate-900 dark:text-slate-100">{agent.name}</h3>
          <VerificationBadge status={agent.verification_status} />
        </div>
        {agent.owner_name && (
          <p className="text-sm text-slate-500 dark:text-slate-400">by {agent.owner_name}</p>
        )}
        <p className="line-clamp-2 text-sm text-slate-600 dark:text-slate-300">
          {agent.description || 'No description provided.'}
        </p>
        {agent.skills.length > 0 && (
          <div className="flex flex-wrap gap-1.5">
            {shownSkills.map((skill) => (
              <Badge key={skill.id} tone="brand">
                {skill.name}
              </Badge>
            ))}
            {extraCount > 0 && <Badge tone="gray">+{extraCount} more</Badge>}
          </div>
        )}
        <p className="mt-auto pt-2 text-xs text-slate-400 dark:text-slate-500">
          registered {formatDate(agent.created_at)}
        </p>
      </Card>
    </Link>
  )
}
