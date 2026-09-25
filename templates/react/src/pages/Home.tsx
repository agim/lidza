import { useQuery } from '@tanstack/react-query'
import { getHealth } from '../api'

export function Home() {
  const health = useQuery({ queryKey: ['health'], queryFn: getHealth })

  return (
    <>
      <h1>__LIDZA_APP_NAME__</h1>
      <p>React frontend, Go control plane, one port.</p>
      <section>
        <h2>GET /api/v1/health</h2>
        {health.isPending && <p>Loading…</p>}
        {health.isError && <p className="error">{String(health.error)}</p>}
        {health.data && (
          <pre>{JSON.stringify(health.data, null, 2)}</pre>
        )}
      </section>
      <p>
        Edit <code>src/pages/Home.tsx</code> and save: the page updates without a reload.
      </p>
    </>
  )
}
