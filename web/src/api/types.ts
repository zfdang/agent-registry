export interface Skill {
  id: string
  name: string
  description?: string | null
}

export interface AgentCapabilities {
  streaming?: boolean
  pushNotifications?: boolean
  stateTransitionHistory?: boolean
  extensions?: { uri: string; description?: string; required?: boolean }[]
  [key: string]: unknown
}

export type VerificationStatus = 'verified' | 'stale' | 'failed'

export interface Agent {
  id: string
  name: string
  owner_name: string | null
  agent_id: string | null
  url: string
  description: string | null
  skills: Skill[]
  // The agent's own version, e.g. "2.0.0"; null when the card doesn't declare one.
  version: string | null
  // Declared A2A protocol version, e.g. "1.0" or "0.2.5" — from the
  // selected supportedInterfaces entry, or a pre-1.0 card's top-level
  // protocolVersion. Null is meaningful — the card declares no version
  // anywhere — not just a missing field.
  protocol_version: string | null
  verification_status: VerificationStatus
  verified_at: string | null
  created_at: string
  // Full-record-only fields (present on GET /v1/agents/*ref)
  capabilities?: AgentCapabilities | null
  card_url?: string | null
  card_json?: Record<string, unknown> | null
}

export interface AgentListResponse {
  agents: Agent[]
  total: number
  page: number
  page_size: number
}

export interface Stats {
  total_agents: number
  verified_agents: number
  new_this_week: number
  distinct_owners: number
}

export type CheckKind = 'l1_card' | 'l2_protocol'
export type CheckStatus = 'passed' | 'failed'

export interface Check {
  kind: CheckKind
  status: CheckStatus
  detail: Record<string, unknown>
  checked_at: string
  expires_at: string | null
}

export interface VerificationResponse {
  status: VerificationStatus
  verified_at: string | null
  checks: Check[]
}

export interface CardSnapshot {
  id: string
  card_url?: string
  card_hash: string
  source: 'register' | 'update' | 'refresh'
  fetched_at: string
  card_json: Record<string, unknown>
}

export interface CardHistoryResponse {
  snapshots: CardSnapshot[]
}

export type ValidationResult = 'passed' | 'failed'

export interface ExtractedAgent {
  name: string
  owner_name: string | null
  agent_id: string | null
  description: string | null
  skills: Skill[]
  version: string | null
  protocol_version: string | null
}

export interface ValidateResponse {
  validation_id: string
  result: ValidationResult
  registerable: boolean
  mode: 'create' | 'update'
  existing_id: string | null
  expires_at: string
  extracted: ExtractedAgent | null
  checks: Check[]
}

export interface RefreshResponse {
  agent: Agent
  checks: Check[]
}

export interface ErrorCandidate {
  id: string
  url: string
  name: string
}

export interface ApiErrorBody {
  error: {
    code: string
    message: string
    candidates?: ErrorCandidate[]
    [key: string]: unknown
  }
}
