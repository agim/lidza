// The admin pages' behaviour. Loaded in <head> so the theme is set before
// the first paint; everything else waits for the document. The pages work
// without it: forms post, links navigate.
(function () {
  'use strict'
  var KEY = 'lidza-admin-theme'
  var dark = window.matchMedia('(prefers-color-scheme: dark)')

  function preference() {
    try { return localStorage.getItem(KEY) || 'auto' } catch (e) { return 'auto' }
  }
  function apply(pref) {
    var theme = pref === 'auto' ? (dark.matches ? 'dark' : 'light') : pref
    document.documentElement.setAttribute('data-bs-theme', theme)
    document.documentElement.setAttribute('data-admin-theme', pref)
  }
  apply(preference())
  dark.addEventListener('change', function () { if (preference() === 'auto') apply('auto') })

  function all(sel, root) { return Array.prototype.slice.call((root || document).querySelectorAll(sel)) }

  document.addEventListener('DOMContentLoaded', function () {
    // Theme: light, dark, or the system's.
    function markTheme() {
      var pref = preference()
      all('[data-admin-set-theme]').forEach(function (b) {
        var on = b.getAttribute('data-admin-set-theme') === pref
        b.classList.toggle('active', on)
        b.setAttribute('aria-pressed', on ? 'true' : 'false')
      })
    }
    all('[data-admin-set-theme]').forEach(function (b) {
      b.addEventListener('click', function (e) {
        e.preventDefault()
        var pref = b.getAttribute('data-admin-set-theme')
        try { localStorage.setItem(KEY, pref) } catch (err) { /* private mode: this page only */ }
        apply(pref)
        markTheme()
      })
    })
    markTheme()

    // Destructive actions ask first.
    all('form[data-admin-confirm]').forEach(function (f) {
      f.addEventListener('submit', function (e) {
        if (!window.confirm(f.getAttribute('data-admin-confirm'))) e.preventDefault()
      })
    })

    // Show or hide what is typed into a secret field.
    all('[data-admin-reveal]').forEach(function (b) {
      var input = document.getElementById(b.getAttribute('data-admin-reveal'))
      if (!input) return
      b.addEventListener('click', function () {
        var show = input.type === 'password'
        input.type = show ? 'text' : 'password'
        b.setAttribute('aria-pressed', show ? 'true' : 'false')
        b.setAttribute('aria-label', show ? 'Hide' : 'Show')
        all('.admin-when-hidden', b).forEach(function (el) { el.classList.toggle('d-none', show) })
        all('.admin-when-shown', b).forEach(function (el) { el.classList.toggle('d-none', !show) })
      })
    })

    // Sign out through the auth pack's route, then leave the admin pages.
    all('[data-admin-signout]').forEach(function (b) {
      b.addEventListener('click', function (e) {
        e.preventDefault()
        fetch(b.getAttribute('data-admin-signout'), { method: 'POST', credentials: 'same-origin', headers: { 'Content-Type': 'application/json' } })
          .finally(function () { window.location.href = '/' })
      })
    })

    // Filter a list as you type: items carry data-admin-search.
    all('[data-admin-filter]').forEach(function (input) {
      var list = document.getElementById(input.getAttribute('data-admin-filter'))
      if (!list) return
      var empty = list.querySelector('[data-admin-empty]')
      function run() {
        var q = input.value.trim().toLowerCase()
        var shown = 0
        all('[data-admin-search]', list).forEach(function (item) {
          var hit = !q || item.getAttribute('data-admin-search').indexOf(q) >= 0
          item.classList.toggle('d-none', !hit)
          if (hit) shown++
        })
        if (empty) empty.classList.toggle('d-none', shown > 0)
      }
      input.addEventListener('input', run)
      input.form && input.form.addEventListener('submit', function (e) { e.preventDefault(); run() })
      run()
    })

    // A section's selector (the provider) shows the fields that apply.
    all('form[data-admin-selector]').forEach(function (form) {
      var name = form.getAttribute('data-admin-selector')
      function chosen() {
        var out = []
        all('[name="' + name + '"]', form).forEach(function (el) {
          if ((el.type === 'radio' || el.type === 'checkbox') ? el.checked : el.value) out.push(el.value)
        })
        return out
      }
      // The console links sit beside the form, outside it.
      var aside = document.querySelectorAll('[data-admin-nolinks]')
      function run() {
        var values = chosen()
        var links = 0
        all('[data-for]').forEach(function (el) {
          var wanted = el.getAttribute('data-for').split(',')
          var hit = wanted.some(function (v) { return values.indexOf(v) >= 0 })
          el.classList.toggle('d-none', !hit)
          if (hit && el.tagName === 'A') links++
        })
        aside.forEach(function (el) { el.classList.toggle('d-none', links > 0) })
        // Placeholders follow the provider (its default model, its key shape).
        all('[data-placeholders]', form).forEach(function (el) {
          var map = {}
          el.getAttribute('data-placeholders').split('|').forEach(function (pair) {
            var i = pair.indexOf('=')
            if (i > 0) map[pair.slice(0, i)] = pair.slice(i + 1)
          })
          for (var i = 0; i < values.length; i++) {
            if (map[values[i]] !== undefined) { el.placeholder = map[values[i]]; return }
          }
        })
      }
      form.addEventListener('change', function (e) { if (e.target.name === name) run() })
      run()
    })

    // Unsaved changes: say so, and ask before leaving the page.
    all('form[data-admin-dirty]').forEach(function (form) {
      var dirty = false
      var note = form.querySelector('[data-admin-dirty-note]')
      function mark(on) {
        dirty = on
        if (note) note.classList.toggle('d-none', !on)
      }
      form.addEventListener('input', function () { mark(true) })
      form.addEventListener('change', function () { mark(true) })
      form.addEventListener('reset', function () {
        setTimeout(function () { mark(false); form.dispatchEvent(new Event('change')); mark(false) }, 0)
      })
      form.addEventListener('submit', function () { dirty = false })
      window.addEventListener('beforeunload', function (e) {
        if (dirty) { e.preventDefault(); e.returnValue = '' }
      })
    })
  })
})()
