import type { Check, CheckKind } from '../../api/types'

export const CHECK_LABELS: Record<CheckKind, string> = {
  l1_card: 'L1 · Agent card',
  l2_id: 'L2 · Agent ID',
  l3_protocol: 'L3 · A2A protocol',
}

/**
 * The API only guarantees a structured `detail` object per check (docs/04);
 * it doesn't promise a single human-readable message field. This derives a
 * short summary line from the fields the docs show in practice, falling
 * back to a generic note so the row always has something legible — the raw
 * detail is always available via the expandable JSON view too.
 */
export function summarizeCheck(check: Check): string {
  const d = check.detail ?? {}
  const note = (d.note ?? d.message ?? d.error) as string | undefined

  switch (check.kind) {
    case 'l1_card':
      if (check.status === 'passed') {
        return `found at ${String(d.card_url ?? 'the declared card URL')}`
      }
      return note ?? 'agent card not found'
    case 'l2_id':
      if (check.status === 'passed') {
        return d.declared ? `agent ID confirmed: ${String(d.declared)}` : 'agent ID confirmed'
      }
      return note ?? 'card does not declare an agent ID — you can still register'
    case 'l3_protocol':
      if (check.status === 'passed') {
        return `endpoint answered a JSON-RPC ${String(d.jsonrpc ?? '2.0')} envelope`
      }
      return note ?? 'A2A protocol probe failed'
    default:
      return note ?? 'no additional detail'
  }
}
