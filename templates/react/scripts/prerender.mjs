// Writes static HTML for every parameterless route into dist/, using the
// server bundle from `vite build --ssr`. Run by `npm run build`.
import { mkdir, readFile, rm, writeFile } from 'node:fs/promises'
import { dirname, join } from 'node:path'
import { pathToFileURL } from 'node:url'

const dist = 'dist'
const server = join(dist, '.server')
const { render, staticPaths } = await import(pathToFileURL(join(process.cwd(), server, 'entry-server.js')).href)
const template = await readFile(join(dist, 'index.html'), 'utf8')
const marker = '<div id="root"></div>'
if (!template.includes(marker)) throw new Error('index.html has no <div id="root"></div>')

for (const path of staticPaths()) {
  const html = await render(path)
  const page = template.replace(marker, `<div id="root">${html}</div>`)
  const file = path === '/' ? join(dist, 'index.html') : join(dist, path, 'index.html')
  await mkdir(dirname(file), { recursive: true })
  await writeFile(file, page)
  console.log(`prerendered ${path}`)
}
await rm(server, { recursive: true, force: true })
