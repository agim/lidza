import { useQuery } from '@tanstack/react-query'
import { api } from '@lidza/client'

export function Home() {
  const health = useQuery({ queryKey: ['health'], queryFn: () => api.health() })
  const greeting = useQuery({ queryKey: ['hello', 'world'], queryFn: () => api.hello({ name: 'world' }) })

  return (
    <>
      <h1>__LIDZA_APP_NAME__</h1>
      <p>React frontend, Go control plane, one port.</p>
      <section>
        <h2>api.hello: GET /api/v1/hello/world</h2>
        {greeting.isPending && <p>Loading…</p>}
        {greeting.isError && <p className="error">{String(greeting.error)}</p>}
        {greeting.data && <p>{greeting.data.message}</p>}
      </section>
      <section>
        <h2>api.health: GET /api/v1/health</h2>
        {health.data && <pre>{JSON.stringify(health.data, null, 2)}</pre>}
      </section>
      <p>
        Edit <code>src/pages/Home.tsx</code> and save: the page updates without a reload.
        Change <code>Greeting</code> in <code>schema.lidza</code>: the types in <code>@lidza/client</code> follow.
      </p>
    </>
  )
}
