import { GitFork } from 'lucide-react'

export function Footer() {
  return (
    <footer className="border-t border-slate-200 py-8 text-sm text-slate-500 dark:border-slate-800 dark:text-slate-400">
      <div className="mx-auto flex max-w-6xl flex-col items-center gap-3 px-4 text-center sm:flex-row sm:justify-between sm:text-left">
        <a
          href="https://github.com/zfdang/agent-registry"
          target="_blank"
          rel="noopener nofollow"
          className="flex items-center gap-1.5 hover:text-slate-700 dark:hover:text-slate-200"
        >
          <GitFork className="size-4" />
          github.com/zfdang/agent-registry
        </a>
        <p>
          Agents can talk to this registry over{' '}
          <code className="rounded bg-slate-100 px-1 py-0.5 text-xs dark:bg-slate-800">/mcp</code> or
          discover it via its own A2A card at{' '}
          <code className="rounded bg-slate-100 px-1 py-0.5 text-xs dark:bg-slate-800">
            /.well-known/agent-card.json
          </code>
          .
        </p>
      </div>
    </footer>
  )
}
