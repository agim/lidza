<script lang="ts">
  import { searchParam, setSearch } from '../../router.svelte'
  import { categories, items } from './data'

  // The list's filter and search are search parameters
  // (/items?category=seating&q=oak), so the view can be linked, reloaded
  // and gone back to. setSearch replaces the history entry: Back leaves
  // the list instead of undoing each keystroke. The route is not
  // prerendered (prerender: false in src/routes.ts): its first render
  // reads the address.
  const category = $derived(searchParam('category', 'all'))
  const q = $derived(searchParam('q'))
  const shown = $derived(items.filter((item) => (category === 'all' || item.category === category) && item.name.toLowerCase().includes(q.toLowerCase())))
</script>

<h1>Items</h1>
<div class="toolbar">
  <input type="search" aria-label="Search items" placeholder="Search items" value={q} oninput={(e) => setSearch({ q: e.currentTarget.value })} />
  <select aria-label="Category" value={category} onchange={(e) => setSearch({ category: e.currentTarget.value }, { defaults: { category: 'all' } })}>
    <option value="all">Category: All</option>
    {#each categories as c (c)}
      <option value={c}>Category: {c}</option>
    {/each}
  </select>
</div>
<ul class="list">
  {#each shown as item (item.slug)}
    <li>
      <a href="/items/{item.slug}">{item.name}</a>
      <p>{item.summary}</p>
    </li>
  {:else}
    <li>No items match.</li>
  {/each}
</ul>
