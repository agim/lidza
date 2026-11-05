// Writes the markup of every route without :params into dist/
// (dist/index.html, dist/<path>/index.html), using the server bundle from
// `vite build --ssr`, and keeps the bare page as dist/.server/index.html:
// the binary serves it to every other path, where main.ts mounts the app.
// Run by `npm run build`; main.ts hydrates a prerendered page.
//
// With locales/<lang>.json the pages are rendered in the default locale
// (I18N_DEFAULT, else en, else the first). With more than one locale each
// page is also written once per locale under dist/.locales/<lang>/, with
// that locale's shell (shell.html) and dist/.locales/manifest.json; the
// binary serves the visitor's.
import { existsSync, readFileSync } from 'node:fs'
import { mkdir, readFile, rm, writeFile } from 'node:fs/promises'
import { dirname, join } from 'node:path'
import { pathToFileURL } from 'node:url'

const dist = 'dist'
const localesDir = join(dist, '.locales')
const { renderPage, renderShell, paths, locales } = await import(pathToFileURL(join(process.cwd(), dist, '.server', 'entry-server.js')).href)
const template = await readFile(join(dist, 'index.html'), 'utf8')
const langs = locales()
const fallback = defaultLocale(langs)

const write = async (file, html) => {
  await mkdir(dirname(file), { recursive: true })
  await writeFile(file, html)
}
const fileFor = (base, path) => (path === '/' ? join(base, 'index.html') : join(base, path, 'index.html'))

await rm(localesDir, { recursive: true, force: true })
await write(join(dist, '.server', 'index.html'), await renderShell(template, fallback))
for (const path of paths()) {
  if (langs.length > 1) {
    for (const lang of langs) {
      await write(fileFor(join(localesDir, lang), path), await renderPage(template, lang, path))
      console.log(`prerendered ${path} (${lang})`)
    }
  }
  await write(fileFor(dist, path), await renderPage(template, fallback, path))
  console.log(`prerendered ${path}`)
}
if (langs.length > 1) {
  for (const lang of langs) await write(join(localesDir, lang, 'shell.html'), await renderShell(template, lang))
  await writeFile(join(localesDir, 'manifest.json'), JSON.stringify({ default: fallback, locales: langs }) + '\n')
}

// defaultLocale is I18N_DEFAULT (the environment, then .env), as the i18n
// pack reads it, when a catalog exists for it; else en; else the first.
function defaultLocale(all) {
  if (all.length === 0) return undefined
  let wanted = process.env.I18N_DEFAULT
  if (!wanted && existsSync('.env')) wanted = readFileSync('.env', 'utf8').match(/^I18N_DEFAULT=\s*"?([^"\s#]+)/m)?.[1]
  const find = (tag) => tag && all.find((l) => l.toLowerCase() === tag.toLowerCase())
  return find(wanted) ?? find('en') ?? all[0]
}
