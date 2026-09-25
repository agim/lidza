import { useQuery } from '@tanstack/react-query'
import { api } from '@lidza/client'

export function Home() {
  const health = useQuery({ queryKey: ['health'], queryFn: () => api.health() })
  const greeting = useQuery({ queryKey: ['hello', 'world'], queryFn: () => api.hello({ name: 'world' }) })

  return (
    <div className="space-y-6">
      <header>
        <h1 className="text-3xl font-semibold tracking-tight">__LIDZA_APP_NAME__</h1>
        <p className="text-muted">React frontend, Go control plane, one port.</p>
      </header>
      <section className="rounded-lg border border-line bg-white p-4">
        <h2 className="mb-2 text-lg font-medium">api.hello: GET /api/v1/hello/world</h2>
        {greeting.isPending && <p className="text-muted">Loading…</p>}
        {greeting.isError && <p className="text-danger">{String(greeting.error)}</p>}
        {greeting.data && <p>{greeting.data.message}</p>}
      </section>
      <section className="rounded-lg border border-line bg-white p-4">
        <h2 className="mb-2 text-lg font-medium">api.health: GET /api/v1/health</h2>
        {health.data && (
          <pre className="overflow-x-auto rounded bg-surface p-3 text-sm">{JSON.stringify(health.data, null, 2)}</pre>
        )}
      </section>
      <p className="text-sm text-muted">
        Edit <code className="rounded bg-surface px-1">src/pages/Home.tsx</code> and save: the page updates without a reload.
        Change <code className="rounded bg-surface px-1">Greeting</code> in <code className="rounded bg-surface px-1">schema.lidza</code>: the types in{' '}
        <code className="rounded bg-surface px-1">@lidza/client</code> follow.
      </p>
    </div>
  )
}
