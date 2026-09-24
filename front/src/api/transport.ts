import type { DescService } from '@bufbuild/protobuf'
import {
  Code,
  ConnectError,
  createClient,
  type Interceptor,
} from '@connectrpc/connect'
import { createConnectTransport } from '@connectrpc/connect-web'
import { TOKEN_KEY } from '@/lib/constants'

export const BASE_URL = import.meta.env.VITE_API_BASE_URL || ''

// authInterceptor injects the Authorization header on every Connect RPC. It also rewrites 401 / Unauthenticated errors into a
// hard redirect to /sign-in so existing React Query callers don't have to
// special-case auth failures.
const authInterceptor: Interceptor = (next) => async (req) => {
  const token = localStorage.getItem(TOKEN_KEY)
  if (token) req.header.set('Authorization', `Bearer ${token}`)

  try {
    return await next(req)
  } catch (err) {
    if (err instanceof ConnectError && err.code === Code.Unauthenticated) {
      // Don't trash the session when a deliberate "Me" check probes auth on
      // the login page — the sign-in page handles its own redirects.
      if (!isProbeRequest(req.url)) {
        localStorage.removeItem(TOKEN_KEY)
        if (
          typeof window !== 'undefined' &&
          window.location.pathname !== '/sign-in'
        ) {
          window.location.href = '/sign-in'
        }
      }
    }
    throw err
  }
}

const probeUrls = new Set<string>([
  '/neobox.v1.AuthService/Me',
  '/neobox.v1.AuthService/Login',
  '/neobox.v1.AuthService/ListOAuthProviders',
  '/neobox.v1.AuthService/BeginOAuthFlow',
  '/neobox.v1.AuthService/CompleteOAuthFlow',
])

function isProbeRequest(url: string): boolean {
  for (const path of probeUrls) {
    if (url.endsWith(path)) return true
  }
  return false
}

export const transport = createConnectTransport({
  baseUrl: `${BASE_URL}/api`,
  // Use binary protobuf on the wire (Content-Type: application/proto). Roughly
  // 30–50% smaller payloads + faster parse than the default JSON encoding;
  // DevTools Network shows the body as binary so debugging falls back to
  // breakpoints or `connect.Code` / `err.message` inspection.
  useBinaryFormat: true,
  interceptors: [authInterceptor],
})

// makeClient is a thin wrapper to keep import sites short.
export function makeClient<T extends DescService>(service: T) {
  return createClient(service, transport)
}

// Re-export so callers don't have to import @connectrpc/connect themselves.
export { ConnectError, Code }
