import type { HTMLAttributes } from 'react'
import { cn } from '../../lib/cn'

type Tone = 'green' | 'amber' | 'gray' | 'red' | 'brand'

const toneClasses: Record<Tone, string> = {
  green:
    'bg-emerald-50 text-emerald-700 dark:bg-emerald-950 dark:text-emerald-400',
  amber: 'bg-amber-50 text-amber-700 dark:bg-amber-950 dark:text-amber-400',
  gray: 'bg-slate-100 text-slate-600 dark:bg-slate-800 dark:text-slate-400',
  red: 'bg-red-50 text-red-700 dark:bg-red-950 dark:text-red-400',
  brand: 'bg-brand-50 text-brand-700 dark:bg-brand-950 dark:text-brand-300',
}

interface BadgeProps extends HTMLAttributes<HTMLSpanElement> {
  tone?: Tone
}

export function Badge({ tone = 'gray', className, ...props }: BadgeProps) {
  return (
    <span
      className={cn(
        'inline-flex items-center gap-1 rounded-full px-2.5 py-1 text-xs font-medium',
        toneClasses[tone],
        className,
      )}
      {...props}
    />
  )
}
