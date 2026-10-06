// An enhance script: what a static page (staticData.static in
// src/router.tsx) loads instead of the app, named in its route:
// `staticData: { static: true, enhance: ['disclosure'] }`. Keep these
// small and plain (no React): the page works without them, and they add
// behaviour on top.
//
// This one makes a button show and hide the element it controls:
//   <button type="button" aria-expanded="false" aria-controls="menu" data-disclosure>Menu</button>
//   <ul id="menu">...</ul>
// Without the script the list stays visible.
for (const button of document.querySelectorAll<HTMLButtonElement>('button[data-disclosure][aria-controls]')) {
  const target = document.getElementById(button.getAttribute('aria-controls') ?? '')
  if (!target) continue
  const set = (open: boolean) => {
    button.setAttribute('aria-expanded', String(open))
    target.hidden = !open
  }
  set(button.getAttribute('aria-expanded') === 'true')
  button.addEventListener('click', () => set(button.getAttribute('aria-expanded') !== 'true'))
}
