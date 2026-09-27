// Writes static HTML for every parameterless route into dist/, using the
// server bundle from `vite build --ssr`, and keeps that bundle with the
// sidecar script in dist/.server for per-request rendering (LIDZA_SSR=1).
// Run by `npm run build`.
//
// With locales/<lang>.json the pages are rendered in the default locale
// (I18N_DEFAULT, else en, else the first). With more than one locale each
// page is also written once per locale under dist/.locales/<lang>/, with
// that locale's shell (shell.html) and dist/.locales/manifest.json; the
// binary serves the visitor's.
import { copyFile, mkdir, readFile, rm, writeFile } from 'node:fs/promises'
import { existsSync, readFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { pathToFileURL } from 'node:url'

const dist = 'dist'
const server = join(dist, '.server')
const localesDir = join(dist, '.locales')
const { render, renderShell, staticPaths, locales } = await import(pathToFileURL(join(process.cwd(), server, 'entry-server.js')).href)
const template = await readFile(join(dist, 'index.html'), 'utf8')

const langs = locales ? locales() : []
const fallback = defaultLocale(langs)
// null is the plain path, in the default locale.
const variants = langs.length > 1 ? [null, ...langs] : [null]
await rm(localesDir, { recursive: true, force: true })

const write = async (file, html) => {
  await mkdir(dirname(file), { recursive: true })
  await writeFile(file, html)
}

for (const path of staticPaths()) {
  for (const lang of variants) {
    let page
    try {
      page = await render(path, { template, locale: lang ?? fallback })
    } catch (err) {
      // A loader that needs the API cannot run at build time: the path is
      // served as the shell and renders in the browser, or per request with
      // LIDZA_SSR=1.
      console.log(`not prerendered ${path}: ${String(err.message ?? err).split('\n')[0]} (needs LIDZA_SSR=1 or a loader that works without the API)`)
      break
    }
    const base = lang ? join(localesDir, lang) : dist
    await write(path === '/' ? join(base, 'index.html') : join(base, path, 'index.html'), page)
    console.log(`prerendered ${path}${lang ? ` (${lang})` : ''}`)
  }
}
if (langs.length > 1) {
  for (const lang of langs) await write(join(localesDir, lang, 'shell.html'), await renderShell(template, lang))
  await write(join(localesDir, 'manifest.json'), JSON.stringify({ default: fallback, locales: langs }) + '\n')
}
await copyFile(join('scripts', 'ssr-server.mjs'), join(server, 'ssr-server.mjs'))
await writeFile(join(server, 'index.html'), template)

// defaultLocale is I18N_DEFAULT (the environment, then .env), as the i18n
// pack reads it, when a catalog exists for it; else en; else the first.
function defaultLocale(all) {
  if (all.length === 0) return undefined
  let wanted = process.env.I18N_DEFAULT
  if (!wanted && existsSync('.env')) wanted = readFileSync('.env', 'utf8').match(/^I18N_DEFAULT=\s*"?([^"\s#]+)/m)?.[1]
  const find = (tag) => tag && all.find((l) => l.toLowerCase() === tag.toLowerCase())
  return find(wanted) ?? find('en') ?? all[0]
}
