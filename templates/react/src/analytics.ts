// Opt-in error and event reporting to the analytics pack. Enabled by
// VITE_ANALYTICS=1 (see main.tsx); until then nothing is sent.
// - reportError: called by the ErrorBoundary, window.onerror and unhandled
//   promise rejections;
// - track: named product events, e.g. track('signup', { plan: 'free' });
// - pageviews are tracked on every navigation.
// The endpoint is same-origin, no third-party script, no IP stored.

let enabled = false
let disabledAfterMissingEndpoint = false

function post(kind: 'errors' | 'events', body: unknown) {
  if (!enabled || disabledAfterMissingEndpoint) return
  fetch(`/api/v1/analytics/${kind}`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
    keepalive: true,
  })
    .then((res) => {
      if (res.status === 404) disabledAfterMissingEndpoint = true
    })
    .catch(() => {})
}

export function reportError(error: unknown, extra?: Record<string, unknown>) {
  const message = error instanceof Error ? `${error.name}: ${error.message}` : String(error)
  const stack = error instanceof Error ? error.stack : undefined
  post('errors', { message, stack, url: location.pathname + location.search, extra })
}

export function track(name: string, props?: Record<string, unknown>) {
  post('events', { name, props, url: location.pathname + location.search, sessionId: sessionId() })
}

function sessionId(): string {
  try {
    let id = sessionStorage.getItem('lidza.session')
    if (!id) {
      id = Math.random().toString(36).slice(2) + Date.now().toString(36)
      sessionStorage.setItem('lidza.session', id)
    }
    return id
  } catch {
    return ''
  }
}

export function enableAnalytics() {
  if (enabled) return
  enabled = true
  window.addEventListener('error', (event) => reportError(event.error ?? event.message))
  window.addEventListener('unhandledrejection', (event) => reportError(event.reason))
  track('pageview')
  const pushState = history.pushState.bind(history)
  history.pushState = (...args) => {
    pushState(...args)
    track('pageview')
  }
  window.addEventListener('popstate', () => track('pageview'))
}
