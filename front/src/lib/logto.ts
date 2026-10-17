import LogtoClient from '@logto/browser'

export const API_BASE_URL = import.meta.env.VITE_API_BASE_URL || ''
export const CALLBACK_PATH = '/auth/callback'
const returnPathKey = 'neobox_logto_return_path'

interface LogtoSettings {
  endpoint: string
  appId: string
  resource: string
  scopes: string[] | null
}

let initialization:
  | Promise<{
      client: LogtoClient
      settings: LogtoSettings
    }>
  | undefined

// One SDK instance shares refreshes across RPCs and downloads. The SDK owns
// its tokens, PKCE verifier and OAuth state; the app stores no bearer token.
export function getLogto() {
  initialization ??= (async () => {
    const response = await fetch(`${API_BASE_URL}/api/auth/config`)
    if (!response.ok)
      throw new Error('Logto sign-in is not configured or is unavailable.')
    const settings = (await response.json()) as LogtoSettings
    const client = new LogtoClient({
      endpoint: settings.endpoint,
      appId: settings.appId,
      resources: [settings.resource],
      scopes: ['email', ...(settings.scopes ?? [])],
    })
    return { client, settings }
  })().catch((error: unknown) => {
    initialization = undefined
    throw error
  })
  return initialization
}

export async function getAccessToken() {
  const { client, settings } = await getLogto()
  if (!(await client.isAuthenticated())) return undefined
  return client.getAccessToken(settings.resource)
}

export function safeReturnPath(value?: string | null): string {
  if (!value?.startsWith('/') || value.startsWith('//') || value.includes('\\'))
    return '/'
  const base = 'https://neo-box.invalid'
  const url = new URL(value, base)
  if (
    url.origin !== base ||
    url.pathname === CALLBACK_PATH ||
    url.pathname === '/sign-in'
  )
    return '/'
  return url.pathname + url.search + url.hash
}

export async function signIn(redirectTo?: string) {
  sessionStorage.setItem(returnPathKey, safeReturnPath(redirectTo))
  const { client } = await getLogto()
  await client.signIn(`${window.location.origin}${CALLBACK_PATH}`)
}

export async function completeSignIn() {
  const { client } = await getLogto()
  await client.handleSignInCallback(window.location.href)
  const path = safeReturnPath(sessionStorage.getItem(returnPathKey))
  sessionStorage.removeItem(returnPathKey)
  return path
}

export async function signOut() {
  const { client } = await getLogto()
  await client.signOut(`${window.location.origin}/sign-in`)
}
