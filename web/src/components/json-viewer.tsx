import { ChevronRight } from 'lucide-react'

interface JsonViewerProps {
  data: unknown
  summary: string
  defaultOpen?: boolean
}

export function JsonViewer({ data, summary, defaultOpen = false }: JsonViewerProps) {
  return (
    <details
      open={defaultOpen}
      className="group rounded-lg border border-slate-200 dark:border-slate-800"
    >
      <summary className="flex cursor-pointer list-none items-center gap-1.5 px-3 py-2 text-sm font-medium text-slate-600 marker:content-[''] dark:text-slate-300">
        <ChevronRight className="size-4 shrink-0 transition-transform group-open:rotate-90" />
        {summary}
      </summary>
      <pre className="overflow-x-auto border-t border-slate-200 bg-slate-50 px-3 py-2 text-xs text-slate-700 dark:border-slate-800 dark:bg-slate-950 dark:text-slate-300">
        {JSON.stringify(data, null, 2)}
      </pre>
    </details>
  )
}
