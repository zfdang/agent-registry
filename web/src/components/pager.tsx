import { ChevronLeft, ChevronRight } from 'lucide-react'
import { Button } from './ui/button'

interface PagerProps {
  page: number
  pageSize: number
  total: number
  onPageChange: (page: number) => void
}

export function Pager({ page, pageSize, total, onPageChange }: PagerProps) {
  const totalPages = Math.max(1, Math.ceil(total / pageSize))

  return (
    <div className="flex flex-col items-center gap-3 sm:flex-row sm:justify-between">
      <p className="text-sm text-slate-500 dark:text-slate-400">
        {total.toLocaleString()} agent{total === 1 ? '' : 's'}
      </p>
      <div className="flex items-center gap-2">
        <Button
          variant="outline"
          size="sm"
          disabled={page <= 1}
          onClick={() => onPageChange(page - 1)}
        >
          <ChevronLeft className="size-4" />
          Prev
        </Button>
        <span className="text-sm text-slate-500 dark:text-slate-400">
          Page {page} of {totalPages}
        </span>
        <Button
          variant="outline"
          size="sm"
          disabled={page >= totalPages}
          onClick={() => onPageChange(page + 1)}
        >
          Next
          <ChevronRight className="size-4" />
        </Button>
      </div>
    </div>
  )
}
