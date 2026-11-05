// Back links: <a href="/posts" data-back> (src/components/BackLink.astro)
// goes back through history when the previous entry is this site's, so it
// lands where the browser's Back would (the list with its filter and
// scroll); otherwise (opened from a link elsewhere, a new tab) it is the
// link it is, to the parent. Middle-click and "open in new tab" open the
// parent. The layout loads this on every page.

/** previousIsOurs says whether Back stays on this site. */
function previousIsOurs(): boolean {
  // The Navigation API lists this origin's entries only.
  if (typeof navigation !== 'undefined' && navigation.currentEntry) return navigation.canGoBack
  try {
    return history.length > 1 && document.referrer !== '' && new URL(document.referrer).origin === location.origin
  } catch {
    return false
  }
}

export function enhanceBackLinks() {
  document.addEventListener('click', (e) => {
    const a = (e.target as Element | null)?.closest?.('a[data-back]')
    if (!(a instanceof HTMLAnchorElement)) return
    if (e.button !== 0 || e.metaKey || e.ctrlKey || e.shiftKey || e.altKey || e.defaultPrevented || (a.target && a.target !== '_self')) return
    if (!previousIsOurs()) return
    e.preventDefault()
    // lidza:ignore L025 (the previous entry is this site's)
    history.back()
  })
}
