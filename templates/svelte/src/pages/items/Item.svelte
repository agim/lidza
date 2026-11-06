<script lang="ts">
  import BackLink from '../../BackLink.svelte'
  import { isActive } from '../../router.svelte'
  import { findItem } from './data'

  // One item at /items/<slug>, its tabs as paths: /items/<slug> is the
  // overview, /items/<slug>/specs the specs (two routes in src/routes.ts).
  // A tab is a link, so it pushes a history entry and Back returns to the
  // tab before; a reload or a shared link opens the same tab.
  let { params }: { params: Record<string, string> } = $props()
  const item = $derived(findItem(params.slug))
  const base = $derived(`/items/${params.slug}`)
  const specs = $derived(isActive(`${base}/specs`))
</script>

{#if item}
  <!-- Back to where the visitor came from in this app (the list, filtered as they left it); /items when opened directly. -->
  <BackLink href="/items" class="back">All items</BackLink>
  <h1>{item.name}</h1>
  <nav aria-label="Item sections" class="tabs">
    <a href={base} data-status={specs ? undefined : 'active'} aria-current={specs ? undefined : 'page'}>Overview</a>
    <a href="{base}/specs" data-status={specs ? 'active' : undefined} aria-current={specs ? 'page' : undefined}>Specs</a>
  </nav>
  {#if specs}
    <dl>
      {#each item.specs as s (s.label)}
        <dt>{s.label}</dt>
        <dd>{s.value}</dd>
      {/each}
    </dl>
  {:else}
    <p>{item.summary}</p>
  {/if}
{:else}
  <!-- An unknown slug is a page too: it says so and links to the parent. -->
  <h1>No item at this address</h1>
  <p><a href="/items">All items</a></p>
{/if}
