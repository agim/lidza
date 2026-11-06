<script lang="ts">
  import type { Snippet } from 'svelte'
  import { back, plainClick } from './router.svelte'

  // A link back to where the visitor came from in this app, as the
  // browser's Back would (the list with its filters and scroll); href is
  // the parent, used when there is no such entry (opened from a link
  // elsewhere, a new tab) and by middle-click and "open in new tab".
  let { href, class: className = '', children }: { href: string; class?: string; children: Snippet } = $props()

  function onclick(e: MouseEvent) {
    if (!plainClick(e, e.currentTarget as HTMLAnchorElement)) return
    e.preventDefault()
    back(href)
  }
</script>

<a {href} class={className} {onclick}>{@render children()}</a>
