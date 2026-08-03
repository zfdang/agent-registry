import type { ReactNode } from 'react'

const PRE_1_0_TITLE = 'card uses the pre-1.0 format (no supportedInterfaces)'

/**
 * Small, muted "vX.Y.Z" / "A2A x.y" chips — metadata, deliberately subordinate
 * to the (colored) skill chips. `protocol_version: null` is meaningful (the
 * card predates `supportedInterfaces`) so it always renders as "A2A 0.x"
 * rather than being hidden; `version: null` is genuinely absent data, so
 * that chip is simply omitted.
 */
export function AgentMetaChips({
  version,
  protocolVersion,
}: {
  version: string | null
  protocolVersion: string | null
}) {
  return (
    <div className="flex flex-wrap items-center gap-1.5">
      {version && <MetaChip>v{version}</MetaChip>}
      {protocolVersion === null ? (
        <MetaChip title={PRE_1_0_TITLE}>A2A 0.x</MetaChip>
      ) : (
        <MetaChip>A2A {protocolVersion}</MetaChip>
      )}
    </div>
  )
}

function MetaChip({ children, title }: { children: ReactNode; title?: string }) {
  return (
    <span
      title={title}
      className="inline-flex items-center rounded-full bg-slate-100 px-2 py-0.5 text-[11px] font-medium text-slate-500 dark:bg-slate-800 dark:text-slate-400"
    >
      {children}
    </span>
  )
}
