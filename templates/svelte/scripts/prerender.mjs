// Writes the app's markup into dist/index.html, using the server bundle
// from `vite build --ssr`. Run by `npm run build`; main.ts hydrates it.
import { readFile, writeFile } from 'node:fs/promises'
import { join } from 'node:path'
import { pathToFileURL } from 'node:url'

const dist = 'dist'
const { renderApp } = await import(pathToFileURL(join(process.cwd(), dist, '.server', 'entry-server.js')).href)
const file = join(dist, 'index.html')
const template = await readFile(file, 'utf8')
const marker = '<div id="app"></div>'
if (!template.includes(marker)) throw new Error('index.html has no <div id="app"></div>')
await writeFile(file, template.replace(marker, `<div id="app">${renderApp()}</div>`))
console.log('prerendered /')
