<script lang="ts">
  import { onMount } from 'svelte'
  import { api, type Greeting, type Health } from '@lidza/client'

  // Every call to the Go control plane goes through the generated client.
  let greeting = $state<Greeting | null>(null)
  let health = $state<Health | null>(null)
  let error = $state<string | null>(null)

  onMount(async () => {
    try {
      greeting = await api.hello({ name: 'world' })
      health = await api.health()
    } catch (e) {
      error = String(e)
    }
  })
</script>

<main>
  <h1>__LIDZA_APP_NAME__</h1>
  <p>Svelte 5 frontend, Go control plane, one port.</p>
  <section>
    <h2>api.hello: GET /api/v1/hello/world</h2>
    {#if error}
      <p class="error">{error}</p>
    {:else if greeting}
      <p>{greeting.message}</p>
    {:else}
      <p>Loading…</p>
    {/if}
  </section>
  <section>
    <h2>api.health: GET /api/v1/health</h2>
    {#if health}
      <pre>{JSON.stringify(health, null, 2)}</pre>
    {/if}
  </section>
  <p>
    Edit <code>src/App.svelte</code> and save: the page updates without a reload.
    Change <code>Greeting</code> in <code>schema.lidza</code>: the types in <code>@lidza/client</code> follow.
  </p>
</main>
