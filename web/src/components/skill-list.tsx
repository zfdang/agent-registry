import { ChevronRight } from 'lucide-react'
import type { Skill } from '../api/types'

/**
 * The card's declared skills as a collapsible list, collapsed by default —
 * skill lists run long on multi-purpose agents and the summary row already
 * carries the count. Same <details> idiom as JsonViewer.
 */
export function SkillList({ skills }: { skills: Skill[] }) {
  return (
    <details className="group rounded-lg border border-slate-200 dark:border-slate-800">
      <summary className="flex cursor-pointer list-none items-center gap-1.5 px-3 py-2 text-sm font-medium text-slate-600 marker:content-[''] dark:text-slate-300">
        <ChevronRight className="size-4 shrink-0 transition-transform group-open:rotate-90" />
        Skills ({skills.length})
      </summary>
      <ul className="flex flex-col divide-y divide-slate-100 border-t border-slate-200 dark:divide-slate-800 dark:border-slate-800">
        {skills.map((skill) => (
          <li key={skill.id} className="flex flex-col gap-0.5 px-3 py-2">
            <div className="flex flex-wrap items-baseline gap-2">
              <span className="text-sm font-medium text-slate-800 dark:text-slate-100">{skill.name}</span>
              <span className="font-mono text-xs text-slate-400 dark:text-slate-500">{skill.id}</span>
            </div>
            {skill.description && (
              <p className="text-sm text-slate-500 dark:text-slate-400">{skill.description}</p>
            )}
          </li>
        ))}
      </ul>
    </details>
  )
}
