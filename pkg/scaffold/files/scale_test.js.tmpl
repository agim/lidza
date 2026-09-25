// k6 scenario run by `lidza benchmark`: every route the app serves under
// load, with the thresholds a release must meet. Add your own routes.
import http from 'k6/http'
import { check, sleep } from 'k6'

export const options = {
  thresholds: {
    http_req_failed: ['rate<0.01'],
    http_req_duration: ['p(95)<250'],
  },
}

const BASE = __ENV.BASE_URL || 'http://127.0.0.1:3000'

export default function () {
  const health = http.get(`${BASE}/api/v1/health`)
  check(health, { 'health 200': (r) => r.status === 200 })
  const hello = http.get(`${BASE}/api/v1/hello/k6`)
  check(hello, { 'hello 200': (r) => r.status === 200 })
  sleep(0.1)
}
