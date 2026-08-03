import { Link } from '@tanstack/react-router'
import { Bot, Plus } from 'lucide-react'
import { buttonClasses } from './ui/button'

export function TopBar() {
  return (
    <header className="sticky top-0 z-40 border-b border-slate-200 bg-white/80 backdrop-blur dark:border-slate-800 dark:bg-slate-950/80">
      <div className="mx-auto flex h-16 max-w-6xl items-center justify-between px-4">
        <Link
          to="/"
          className="flex items-center gap-2 text-lg font-semibold text-slate-900 dark:text-slate-100"
        >
          <Bot className="size-6 text-brand-600 dark:text-brand-400" />
          Agent Registry
        </Link>
        <Link to="/add" className={buttonClasses('primary', 'md')}>
          <Plus className="size-4" />
          Add your agent
        </Link>
      </div>
    </header>
  )
}
