import { useQuery } from '@tanstack/react-query'
import { api } from '@lidza/client'
import { useTitle } from '../navigation'
import { Notice, Page, Section } from '../ui'

// A page is built from src/ui.tsx: Page gives the title, the lede and the
// rhythm, Section a card; nothing here styles a heading or a box by hand.
export function Home() {
  useTitle()
  const health = useQuery({ queryKey: ['health'], queryFn: () => api.health() })
  const greeting = useQuery({ queryKey: ['hello', 'world'], queryFn: () => api.hello({ name: 'world' }) })

  return (
    <Page title="__LIDZA_APP_NAME__" lede="React frontend, Go control plane, one port.">
      <Section title="api.hello: GET /api/v1/hello/world">
        {greeting.isPending && <p className="text-muted">Loading…</p>}
        {greeting.isError && <Notice tone="danger">{String(greeting.error)}</Notice>}
        {greeting.data && <p>{greeting.data.message}</p>}
      </Section>
      <Section title="api.health: GET /api/v1/health">
        {health.data && <pre className="overflow-x-auto rounded bg-surface p-3 text-sm">{JSON.stringify(health.data, null, 2)}</pre>}
      </Section>
      <p className="text-sm text-muted">
        Edit <code className="rounded bg-surface px-1">src/pages/Home.tsx</code> and save: the page updates without a reload.
        Change <code className="rounded bg-surface px-1">Greeting</code> in <code className="rounded bg-surface px-1">schema.lidza</code>: the types in{' '}
        <code className="rounded bg-surface px-1">@lidza/client</code> follow.
      </p>
    </Page>
  )
}
