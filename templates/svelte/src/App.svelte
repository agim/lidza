<script lang="ts">
  import { address, isActive, match } from './router.svelte'
  import NotFound from './pages/NotFound.svelte'

  // The layout around every page: the route for the current address
  // (src/routes.ts) renders inside <main>.
  const current = $derived(match(address.pathname))
  const links = [
    { href: '/', label: 'Home', exact: true },
    { href: '/items', label: 'Items', exact: false },
    { href: '/about', label: 'About', exact: false },
  ]
</script>

<nav>
  {#each links as link (link.href)}
    {@const active = isActive(link.href, link.exact)}
    <a href={link.href} data-status={active ? 'active' : undefined} aria-current={active ? 'page' : undefined}>{link.label}</a>
  {/each}
</nav>
<main>
  {#if current.route}
    {@const Page = current.route.component}
    <Page params={current.params} />
  {:else}
    <NotFound />
  {/if}
</main>
