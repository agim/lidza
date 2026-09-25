// Writes static HTML for every parameterless route into dist/, using the
// server bundle from `vite build --ssr`, and keeps that bundle with the
// sidecar script in dist/.server for per-request rendering (LIDZA_SSR=1).
// Run by `npm run build`.
import { copyFile, mkdir, readFile, writeFile } from 'node:fs/promises'
import { dirname, join } from 'node:path'
import { pathToFileURL } from 'node:url'

const dist = 'dist'
const server = join(dist, '.server')
const { render, staticPaths } = await import(pathToFileURL(join(process.cwd(), server, 'entry-server.js')).href)
const template = await readFile(join(dist, 'index.html'), 'utf8')

for (const path of staticPaths()) {
  let page
  try {
    page = await render(path, { template })
  } catch (err) {
    // A loader that needs the API cannot run at build time: the path is
    // served as the shell and renders in the browser, or per request with
    // LIDZA_SSR=1.
    console.log(`not prerendered ${path}: ${String(err.message ?? err).split('\n')[0]} (needs LIDZA_SSR=1 or a loader that works without the API)`)
    continue
  }
  const file = path === '/' ? join(dist, 'index.html') : join(dist, path, 'index.html')
  await mkdir(dirname(file), { recursive: true })
  await writeFile(file, page)
  console.log(`prerendered ${path}`)
}
await copyFile(join('scripts', 'ssr-server.mjs'), join(server, 'ssr-server.mjs'))
await writeFile(join(server, 'index.html'), template)
