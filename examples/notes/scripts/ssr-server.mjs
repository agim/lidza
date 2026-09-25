// Per-request rendering sidecar, started by the Go binary when LIDZA_SSR=1:
// listens on the Unix socket given as the first argument and answers
// POST /render {"path", "headers", "apiBase"} with {"html"}: the whole
// page, markup and hydration payload in the built template. Renders run
// one at a time: the generated client's configuration is process-wide, and
// forwarding one request's cookies to loaders must not leak into another.
import { readFile } from 'node:fs/promises'
import { createServer } from 'node:http'
import { dirname, join } from 'node:path'
import { fileURLToPath, pathToFileURL } from 'node:url'

const here = dirname(fileURLToPath(import.meta.url))
const { render } = await import(pathToFileURL(join(here, 'entry-server.js')).href)
const template = await readFile(join(here, 'index.html'), 'utf8')
const socket = process.argv[2]
if (!socket) {
  console.error('usage: node ssr-server.mjs <socket path>')
  process.exit(2)
}

let queue = Promise.resolve()
const serially = (task) => {
  const run = queue.then(task, task)
  queue = run.catch(() => {})
  return run
}

const server = createServer((req, res) => {
  if (req.method !== 'POST' || req.url !== '/render') {
    res.writeHead(404).end()
    return
  }
  let body = ''
  req.on('data', (chunk) => (body += chunk))
  req.on('end', () => {
    serially(async () => {
      const { path, headers, apiBase } = JSON.parse(body)
      const html = await render(path, { headers, apiBase, template })
      res.writeHead(200, { 'Content-Type': 'application/json' })
      res.end(JSON.stringify({ html }))
    }).catch((err) => {
      res.writeHead(500, { 'Content-Type': 'application/json' })
      res.end(JSON.stringify({ error: String(err && err.stack ? err.stack : err) }))
    })
  })
})
server.listen(socket, () => console.log(`ssr sidecar listening on ${socket}`))
for (const signal of ['SIGINT', 'SIGTERM']) {
  process.on(signal, () => server.close(() => process.exit(0)))
}
