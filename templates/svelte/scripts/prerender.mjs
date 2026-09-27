// Writes the app's markup into dist/index.html, using the server bundle
// from `vite build --ssr`. Run by `npm run build`; main.ts hydrates it.
//
// With locales/<lang>.json the page is rendered in the default locale
// (I18N_DEFAULT, else en, else the first). With more than one locale it is
// also written once per locale as dist/.locales/<lang>/index.html, with
// dist/.locales/manifest.json; the binary serves the visitor's.
import { existsSync, readFileSync } from 'node:fs'
import { mkdir, readFile, rm, writeFile } from 'node:fs/promises'
import { join } from 'node:path'
import { pathToFileURL } from 'node:url'

const dist = 'dist'
const localesDir = join(dist, '.locales')
const { renderPage, locales } = await import(pathToFileURL(join(process.cwd(), dist, '.server', 'entry-server.js')).href)
const file = join(dist, 'index.html')
const template = await readFile(file, 'utf8')
const langs = locales()
const fallback = defaultLocale(langs)

await rm(localesDir, { recursive: true, force: true })
if (langs.length > 1) {
  for (const lang of langs) {
    await mkdir(join(localesDir, lang), { recursive: true })
    await writeFile(join(localesDir, lang, 'index.html'), await renderPage(template, lang))
    console.log(`prerendered / (${lang})`)
  }
  await writeFile(join(localesDir, 'manifest.json'), JSON.stringify({ default: fallback, locales: langs }) + '\n')
}
await writeFile(file, await renderPage(template, fallback))
console.log('prerendered /')

// defaultLocale is I18N_DEFAULT (the environment, then .env), as the i18n
// pack reads it, when a catalog exists for it; else en; else the first.
function defaultLocale(all) {
  if (all.length === 0) return undefined
  let wanted = process.env.I18N_DEFAULT
  if (!wanted && existsSync('.env')) wanted = readFileSync('.env', 'utf8').match(/^I18N_DEFAULT=\s*"?([^"\s#]+)/m)?.[1]
  const find = (tag) => tag && all.find((l) => l.toLowerCase() === tag.toLowerCase())
  return find(wanted) ?? find('en') ?? all[0]
}
