// Calls to the Go control plane. Roadmap Phase 3 replaces this file with the
// generated `@lidza/client`; until then keep every fetch here so the swap is
// one import.

export interface Health {
  status: string
  version: string
  time: string
}

export async function getHealth(): Promise<Health> {
  const res = await fetch('/api/v1/health')
  if (!res.ok) throw new Error(`GET /api/v1/health: ${res.status}`)
  return res.json()
}
