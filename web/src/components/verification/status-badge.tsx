import { CheckCircle2, CircleAlert, Clock, XCircle } from 'lucide-react'
import { Badge } from '../ui/badge'
import type { VerificationStatus } from '../../api/types'

const config: Record<
  VerificationStatus,
  { label: string; tone: 'green' | 'amber' | 'gray' | 'red'; icon: typeof CheckCircle2; title?: string }
> = {
  verified: { label: 'Verified', tone: 'green', icon: CheckCircle2 },
  partial: {
    label: 'Partially verified',
    tone: 'amber',
    icon: CircleAlert,
    title: 'Agent card verified. A2A endpoint verification did not pass.',
  },
  stale: { label: 'Stale', tone: 'gray', icon: Clock },
  failed: { label: 'Failed', tone: 'red', icon: XCircle },
}

export function VerificationBadge({ status }: { status: VerificationStatus }) {
  const { label, tone, icon: Icon, title } = config[status]
  return (
    <Badge tone={tone} title={title}>
      <Icon className="size-3.5" />
      {label}
    </Badge>
  )
}
