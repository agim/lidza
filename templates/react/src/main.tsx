import { StrictMode } from 'react'
import { createRoot, hydrateRoot } from 'react-dom/client'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { RouterProvider } from '@tanstack/react-router'
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

const app = (
  <StrictMode>
    <ErrorBoundary>
      <QueryClientProvider client={queryClient}>
        <RouterProvider router={router} />
      </QueryClientProvider>
    </ErrorBoundary>
  </StrictMode>
)

// Prerendered pages arrive with markup: hydrate it; otherwise render.
if (hydrating) {
  hydrateRoot(root, app)
} else {
  createRoot(root).render(app)
}
