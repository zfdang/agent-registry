import { Outlet, createRootRoute } from '@tanstack/react-router'
import { TopBar } from '../components/top-bar'
import { Footer } from '../components/footer'
import { Toaster } from '../components/ui/toaster'

export const rootRoute = createRootRoute({
  component: () => (
    <div className="flex min-h-screen flex-col">
      <TopBar />
      <main className="mx-auto w-full max-w-6xl flex-1 px-4 py-8">
        <Outlet />
      </main>
      <Footer />
      <Toaster />
    </div>
  ),
})
