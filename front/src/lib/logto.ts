import type { AuthUser } from '@/api/auth'

export const API_BASE_URL = import.meta.env.VITE_API_BASE_URL || ''

export interface BrowserSession {
  user: AuthUser
  csrfToken: string
}

let cachedSession: BrowserSession | null | undefined
let loading: Promise<BrowserSession | null> | undefined

// The browser receives only display information and a CSRF token. Logto
// credentials, authorization codes and access/refresh tokens stay on the server.
export function getBrowserSession(): Promise<BrowserSession | null> {
  if (cachedSession !== undefined) return Promise.resolve(cachedSession)
  loading ??= (async () => {
    const response = await fetch(`${API_BASE_URL}/api/auth/session`, {
      credentials: 'include',
      cache: 'no-store',
    })
    if (response.status === 401) {
      cachedSession = null
      return null
    }
    if (!response.ok)
      throw new Error('Unable to load your sign-in session. Please try again.')
    const session = (await response.json()) as BrowserSession
    cachedSession = session
    return session
  })().finally(() => {
    loading = undefined
  })
  return loading
}

export function clearBrowserSession() {
  cachedSession = undefined
}

export async function getCSRFToken() {
  return (await getBrowserSession())?.csrfToken
}

export function safeReturnPath(value?: string | null): string {
  if (!value?.startsWith('/') || value.startsWith('//') || value.includes('\\'))
    return '/'
  const base = 'https://neo-box.invalid'
  const url = new URL(value, base)
  if (
    url.origin !== base ||
    url.pathname === '/auth/callback' ||
    url.pathname.startsWith('/api/') ||
    url.pathname === '/sign-in'
  )
    return '/'
  return url.pathname + url.search + url.hash
}

export async function signIn(redirectTo?: string, reauthenticate = false) {
  const params = new URLSearchParams({ redirect: safeReturnPath(redirectTo) })
  if (reauthenticate) params.set('reauth', '1')
  window.location.assign(`${API_BASE_URL}/api/auth/login?${params}`)
}

export async function signOut() {
  const session = await getBrowserSession()
  if (!session) {
    window.location.assign('/sign-in')
    return
  }
  // A top-level POST lets the backend redirect directly to Logto's logout
  // endpoint, including its protocol ID-token hint, without returning it to JS.
  const form = document.createElement('form')
  form.method = 'post'
  form.action = `${API_BASE_URL}/api/auth/logout`
  const csrf = document.createElement('input')
  csrf.type = 'hidden'
  csrf.name = 'csrf_token'
  csrf.value = session.csrfToken
  form.append(csrf)
  document.body.append(form)
  form.submit()
}
