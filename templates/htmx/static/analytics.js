// Opt-in error and event reporting to the analytics pack, included by the
// layout when ANALYTICS_FRONTEND=1. Reports errors, unhandled promise
// rejections and pageviews (full loads and htmx boosted navigations);
// window.lidzaTrack(name, props) records a named event. Same origin, no
// third-party script, no IP stored.
(function () {
  var disabled = false
  function post(kind, body) {
    if (disabled) return
    fetch('/api/v1/analytics/' + kind, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(body),
      keepalive: true,
    })
      .then(function (res) { if (res.status === 404) disabled = true })
      .catch(function () {})
  }
  function sessionId() {
    try {
      var id = sessionStorage.getItem('lidza.session')
      if (!id) {
        id = Math.random().toString(36).slice(2) + Date.now().toString(36)
        sessionStorage.setItem('lidza.session', id)
      }
      return id
    } catch (e) {
      return ''
    }
  }
  function track(name, props) {
    post('events', { name: name, props: props, url: location.pathname + location.search, sessionId: sessionId() })
  }
  function reportError(error) {
    var message = error instanceof Error ? error.name + ': ' + error.message : String(error)
    post('errors', { message: message, stack: error && error.stack, url: location.pathname + location.search })
  }
  window.lidzaTrack = track
  window.addEventListener('error', function (event) { reportError(event.error || event.message) })
  window.addEventListener('unhandledrejection', function (event) { reportError(event.reason) })
  track('pageview')
  document.body.addEventListener('htmx:pushedIntoHistory', function () { track('pageview') })
})()
