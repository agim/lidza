import { StrictMode } from 'react'
import { createRoot, hydrateRoot } from 'react-dom/client'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { RouterProvider } from '@tanstack/react-router'
import { RouterClient } from '@tanstack/react-router/ssr/client'
import { createAppRouter } from './router'
import { ErrorBoundary } from './ErrorBoundary'
import { announceTimezone } from './timezone'
import { enableAnalytics } from './analytics'
import './index.css'

announceTimezone()
if (import.meta.env.VITE_ANALYTICS === '1') enableAnalytics()

const queryClient = new QueryClient()
const root = document.getElementById('root')!
const hydrating = root.hasChildNodes()
const router = createAppRouter()

// Prerendered and server-rendered pages arrive with markup and the
// router's hydration payload (loader data): RouterClient hydrates both, so
// the loaders do not run again. Otherwise render from scratch.
const app = (
  <StrictMode>
    <ErrorBoundary>
      <QueryClientProvider client={queryClient}>
        {hydrating ? <RouterClient router={router} /> : <RouterProvider router={router} />}
      </QueryClientProvider>
    </ErrorBoundary>
  </StrictMode>
)

if (hydrating) {
  hydrateRoot(root, app)
} else {
  createRoot(root).render(app)
}
