import type {
  Agent,
  AgentListResponse,
  ApiErrorBody,
  CardHistoryResponse,
  RefreshResponse,
  Stats,
  ValidateResponse,
  VerificationResponse,
} from './types'

const BASE_URL = import.meta.env.VITE_API_URL ?? ''

export class ApiError extends Error {
  code: string
  status: number
  candidates?: ApiErrorBody['error']['candidates']

  constructor(status: number, body: ApiErrorBody) {
    super(body.error.message)
    this.name = 'ApiError'
    this.code = body.error.code
    this.status = status
    this.candidates = body.error.candidates
  }
}

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const res = await fetch(`${BASE_URL}${path}`, {
    ...init,
    headers: {
      Accept: 'application/json',
      ...(init?.body ? { 'Content-Type': 'application/json' } : {}),
      ...init?.headers,
    },
  })

  if (!res.ok) {
    let body: ApiErrorBody
    try {
      body = (await res.json()) as ApiErrorBody
    } catch {
      body = { error: { code: 'unknown', message: res.statusText } }
    }
    throw new ApiError(res.status, body)
  }

  if (res.status === 204) return undefined as T
  return (await res.json()) as T
}

// The agent ref can itself contain slashes (host/path form) — encode each
// segment individually so slashes survive but special characters don't break
// the URL.
function encodeRef(ref: string): string {
  return ref.split('/').map(encodeURIComponent).join('/')
}

export interface ListAgentsParams {
  q?: string
  page?: number
  page_size?: number
  status?: 'verified' | 'verified_with_warnings' | 'stale'
  sort?: 'newest' | 'name'
}

export const api = {
  getStats: (): Promise<Stats> => request('/v1/stats'),

  listAgents: (params: ListAgentsParams = {}): Promise<AgentListResponse> => {
    const search = new URLSearchParams()
    if (params.q) search.set('q', params.q)
    if (params.page) search.set('page', String(params.page))
    if (params.page_size) search.set('page_size', String(params.page_size))
    if (params.status) search.set('status', params.status)
    if (params.sort) search.set('sort', params.sort)
    const qs = search.toString()
    return request(`/v1/agents${qs ? `?${qs}` : ''}`)
  },

  getAgent: (ref: string): Promise<Agent> =>
    request(`/v1/agents/${encodeRef(ref)}`),

  getVerification: (ref: string): Promise<VerificationResponse> =>
    request(`/v1/agents/${encodeRef(ref)}/verification`),

  getCardHistory: (ref: string): Promise<CardHistoryResponse> =>
    request(`/v1/agents/${encodeRef(ref)}/cards`),

  validate: (url: string): Promise<ValidateResponse> =>
    request('/v1/agents/validate', {
      method: 'POST',
      body: JSON.stringify({ url }),
    }),

  register: (validationId: string): Promise<Agent> =>
    request('/v1/agents', {
      method: 'POST',
      body: JSON.stringify({ validation_id: validationId }),
    }),

  refresh: (ref: string): Promise<RefreshResponse> =>
    request(`/v1/agents/${encodeRef(ref)}/refresh`, { method: 'POST' }),
}
