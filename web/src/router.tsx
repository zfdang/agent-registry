import { createRouter } from '@tanstack/react-router'
import { rootRoute } from './routes/root'
import { indexRoute } from './routes/index'
import { agentDetailRoute } from './routes/agent-detail'
import { addRoute } from './routes/add'

const routeTree = rootRoute.addChildren([indexRoute, agentDetailRoute, addRoute])

export const router = createRouter({ routeTree })

declare module '@tanstack/react-router' {
  interface Register {
    router: typeof router
  }
}
