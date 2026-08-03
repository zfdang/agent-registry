import { CheckCircle2, Loader2, AlertTriangle, XCircle } from 'lucide-react'
import type { Check, CheckKind } from '../../api/types'
import { CHECK_LABELS, summarizeCheck } from './check-summary'
import { JsonViewer } from '../json-viewer'
import { formatDateTime } from '../../lib/format-date'

const ALL_KINDS: CheckKind[] = ['l1_card', 'l2_id', 'l3_protocol']

const statusIcon = {
  passed: <CheckCircle2 className="size-4 text-emerald-600 dark:text-emerald-400" />,
  warning: <AlertTriangle className="size-4 text-amber-600 dark:text-amber-400" />,
  failed: <XCircle className="size-4 text-red-600 dark:text-red-400" />,
}

interface CheckListProps {
  checks?: Check[]
  pending?: boolean
  showExpiry?: boolean
}

export function CheckList({ checks, pending = false, showExpiry = false }: CheckListProps) {
  return (
    <ul aria-live="polite" className="flex flex-col gap-2">
      {ALL_KINDS.map((kind) => {
        const check = checks?.find((c) => c.kind === kind)
        const label = CHECK_LABELS[kind]

        if (!check) {
          return (
            <li
              key={kind}
              className="flex items-center gap-2 rounded-lg border border-slate-200 px-3 py-2 text-sm dark:border-slate-800"
            >
              {pending ? (
                <Loader2 className="size-4 shrink-0 animate-spin text-slate-400" />
              ) : (
                <span className="size-4 shrink-0" />
              )}
              <span className="font-medium text-slate-500 dark:text-slate-400">{label}</span>
              <span className="text-slate-400 dark:text-slate-500">
                {pending ? 'running…' : 'not yet run'}
              </span>
            </li>
          )
        }

        return (
          <li key={kind} className="rounded-lg border border-slate-200 px-3 py-2 dark:border-slate-800">
            <div className="flex flex-wrap items-center gap-2 text-sm">
              {statusIcon[check.status]}
              <span className="font-medium text-slate-800 dark:text-slate-100">{label}</span>
              <span className="text-slate-500 dark:text-slate-400">{summarizeCheck(check)}</span>
              {showExpiry && check.expires_at && (
                <span className="ml-auto shrink-0 text-xs text-slate-400 dark:text-slate-500">
                  expires {formatDateTime(check.expires_at)}
                </span>
              )}
            </div>
            <div className="mt-2">
              <JsonViewer data={check.detail} summary="Detail" />
            </div>
          </li>
        )
      })}
    </ul>
  )
}
