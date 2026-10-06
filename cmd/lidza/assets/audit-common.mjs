// What every lidza audit shares: the settings the CLI passes and signing
// a browser context in. Written into .lidza/ beside the audit script.
import { pathToFileURL } from 'node:url'

export const base = process.env.BASE_URL
export const routes = JSON.parse(process.env.AUDIT_ROUTES)
export const storageState = process.env.AUDIT_STORAGE_STATE || undefined

const register = process.env.AUDIT_SIGN_IN === '1'
const loginModule = process.env.AUDIT_LOGIN || ''
const login = loginModule ? (await import(pathToFileURL(loginModule).href)).default : null

// signIn signs a new context in: with the storage state it was made
// with, the app's own login module (a fixture user, a test login route),
// or a throwaway user registered in the test database. It reports
// whether the context is signed in; false when nothing asks for it.
export async function signIn(context) {
  if (storageState) return true
  if (login) {
    const page = await context.newPage()
    try {
      await login(page, base)
      return true
    } catch (e) {
      console.error('[audit] the login module failed: ' + String(e.message || e).split('\n')[0])
      return false
    } finally {
      await page.close()
    }
  }
  if (register) {
    const email = `audit-${Date.now()}-${Math.random().toString(36).slice(2, 8)}@example.com`
    const res = await context.request.post(base + '/api/v1/auth/register', {
      data: { email, password: 'audit-layout-password', name: 'Audit' },
    })
    return res.ok()
  }
  return false
}
