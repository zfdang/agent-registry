import { QueryClient } from '@tanstack/react-query'
import type { ListAgentsParams } from './client'

export const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      staleTime: 30_000,
      retry: 1,
    },
  },
})

export const queryKeys = {
  stats: ['stats'] as const,
  agents: (params: ListAgentsParams) => ['agents', params] as const,
  agent: (ref: string) => ['agent', ref] as const,
  verification: (ref: string) => ['verification', ref] as const,
  cards: (ref: string) => ['cards', ref] as const,
}
