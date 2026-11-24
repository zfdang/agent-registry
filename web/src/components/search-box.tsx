import { useEffect, useState } from 'react'
import { Search } from 'lucide-react'
import { Input } from './ui/input'
import { cn } from '../lib/cn'
import type { VerificationStatus } from '../api/types'

type StatusFilter = Extract<VerificationStatus, 'verified' | 'partial' | 'stale'>

const STATUS_CHIPS: { value: StatusFilter | undefined; label: string }[] = [
  { value: undefined, label: 'All' },
  { value: 'verified', label: 'Verified' },
  { value: 'partial', label: 'Partially verified' },
  { value: 'stale', label: 'Stale' },
]

interface SearchBoxProps {
  value: string
  onChange: (q: string) => void
  status: StatusFilter | undefined
  onStatusChange: (status: StatusFilter | undefined) => void
}

export function SearchBox({ value, onChange, status, onStatusChange }: SearchBoxProps) {
  const [draft, setDraft] = useState(value)

  // Keep the input in sync if the URL-driven value changes externally
  // (e.g. back/forward navigation).
  useEffect(() => {
    setDraft(value)
  }, [value])

  useEffect(() => {
    const timer = setTimeout(() => {
      if (draft !== value) onChange(draft)
    }, 300)
    return () => clearTimeout(timer)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [draft])

  return (
    <div className="flex flex-col gap-3">
      <div className="relative">
        <Search className="pointer-events-none absolute left-3 top-1/2 size-5 -translate-y-1/2 text-slate-400" />
        <Input
          value={draft}
          onChange={(e) => setDraft(e.target.value)}
          placeholder="Search by name, skill, owner, URL, or card content…"
          className="h-14 pl-11 text-base"
          aria-label="Search agents"
        />
      </div>
      <div className="flex flex-wrap gap-2">
        {STATUS_CHIPS.map((chip) => (
          <button
            key={chip.label}
            type="button"
            onClick={() => onStatusChange(chip.value)}
            className={cn(
              'rounded-full px-3 py-1 text-xs font-medium transition-colors',
              status === chip.value
                ? 'bg-brand-600 text-white'
                : 'bg-slate-100 text-slate-600 hover:bg-slate-200 dark:bg-slate-800 dark:text-slate-300 dark:hover:bg-slate-700',
            )}
          >
            {chip.label}
          </button>
        ))}
      </div>
    </div>
  )
}
