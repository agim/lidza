// Back links: <a href="/items" data-back> goes back through history when
// the previous entry is this site's, so it lands where the browser's Back
// would (the list with its filter and scroll); otherwise (opened from a
// link elsewhere, a new tab) it is the link it is, to the parent.
// Middle-click and "open in new tab" open the parent. A file, not an
// inline script, so the default Content-Security-Policy holds.
(function () {
  function previousIsOurs() {
    // The Navigation API lists this origin's entries only.
    if (window.navigation && navigation.currentEntry) return navigation.canGoBack
    try {
      return history.length > 1 && document.referrer !== '' && new URL(document.referrer).origin === location.origin
    } catch (e) {
      return false
    }
  }
  document.addEventListener('click', function (e) {
    var a = e.target && e.target.closest && e.target.closest('a[data-back]')
    if (!a || e.button !== 0 || e.metaKey || e.ctrlKey || e.shiftKey || e.altKey || e.defaultPrevented || (a.target && a.target !== '_self')) return
    if (!previousIsOurs()) return
    e.preventDefault()
    // lidza:ignore L025 (the previous entry is this site's)
    history.back()
  })
})()
