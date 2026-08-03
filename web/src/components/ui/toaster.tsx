import { CheckCircle2, Info, XCircle, X } from 'lucide-react'
import { useToasts } from '../../lib/toast'
import { cn } from '../../lib/cn'

const icons = {
  default: Info,
  success: CheckCircle2,
  error: XCircle,
}

const toneClasses = {
  default: 'border-slate-200 bg-white text-slate-700 dark:border-slate-700 dark:bg-slate-900 dark:text-slate-200',
  success:
    'border-emerald-200 bg-emerald-50 text-emerald-800 dark:border-emerald-900 dark:bg-emerald-950 dark:text-emerald-300',
  error: 'border-red-200 bg-red-50 text-red-800 dark:border-red-900 dark:bg-red-950 dark:text-red-300',
}

export function Toaster() {
  const { items, dismiss } = useToasts()

  return (
    <div
      aria-live="polite"
      className="pointer-events-none fixed bottom-4 right-4 z-50 flex w-full max-w-sm flex-col gap-2"
    >
      {items.map((item) => {
        const Icon = icons[item.variant]
        return (
          <div
            key={item.id}
            className={cn(
              'pointer-events-auto flex items-start gap-2 rounded-lg border px-4 py-3 text-sm shadow-lg',
              toneClasses[item.variant],
            )}
          >
            <Icon className="mt-0.5 size-4 shrink-0" />
            <span className="flex-1">{item.message}</span>
            <button
              type="button"
              onClick={() => dismiss(item.id)}
              aria-label="Dismiss"
              className="shrink-0 opacity-60 hover:opacity-100"
            >
              <X className="size-4" />
            </button>
          </div>
        )
      })}
    </div>
  )
}
