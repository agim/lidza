// Writes static HTML for every parameterless route into dist/, using the
// server bundle from `vite build --ssr`, and keeps that bundle with the
// sidecar script in dist/.server for per-request rendering (LIDZA_SSR=1).
// Run by `npm run build`.
//
// A route with staticData.static (src/router.tsx) is written without the
// client runtime: every script but JSON-LD is dropped, so the page is its
// HTML and CSS, and the src/enhance scripts it names are added alone.
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
const { render, renderShell, staticPaths, pageModes, locales } = await import(pathToFileURL(join(process.cwd(), server, 'entry-server.js')).href)
const template = await readFile(join(dist, 'index.html'), 'utf8')
const modes = pageModes ? pageModes() : {}
const manifestFile = join(dist, '.vite', 'manifest.json')
const manifest = existsSync(manifestFile) ? JSON.parse(readFileSync(manifestFile, 'utf8')) : {}

// lighten is a static page: the scripts (the app, its preloads, the
// router's hydration payload, the i18n catalog) dropped but JSON-LD, and
// the page's enhance scripts added.
const lighten = (html, path) => {
  const mode = modes[path]
  if (!mode?.static) return html
  html = html.replace(/<script\b(?![^>]*type="application\/ld\+json")[^>]*>[\s\S]*?<\/script>/gi, '')
  html = html.replace(/<link\b[^>]*rel="modulepreload"[^>]*>/gi, '')
  const tags = (mode.enhance ?? []).map((name) => {
    const entry = manifest[`src/enhance/${name}.ts`]
    if (!entry) throw new Error(`${path}: enhance "${name}" has no src/enhance/${name}.ts`)
    return `<script type="module" src="/${entry.file}"></script>`
  })
  return html.replace('</body>', tags.join('') + '</body>')
}

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
    // Outside the try: a static page naming a missing enhance script
    // fails the build.
    page = lighten(page, path)
    const base = lang ? join(localesDir, lang) : dist
    await write(path === '/' ? join(base, 'index.html') : join(base, path, 'index.html'), page)
    console.log(`prerendered ${path}${lang ? ` (${lang})` : ''}${modes[path]?.static ? ' (static: no client runtime)' : ''}`)
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
