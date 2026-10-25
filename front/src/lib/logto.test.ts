import { beforeEach, describe, expect, it, vi } from 'vitest'

const browserSession = {
  user: { id: 'personal', username: 'owner', displayName: 'Logto Owner' },
  csrfToken: 'csrf-secret',
}

beforeEach(() => {
  vi.resetModules()
  vi.stubGlobal(
    'fetch',
    vi.fn().mockResolvedValue({
      ok: true,
      status: 200,
      json: async () => browserSession,
    })
  )
  vi.stubGlobal('window', { location: { assign: vi.fn() } })
})

describe('server-side Logto session', () => {
  it('uses a credentialed session probe, shared by simultaneous requests', async () => {
    const { getBrowserSession, getCSRFToken } = await import('./logto')
    const [session, csrf] = await Promise.all([
      getBrowserSession(),
      getCSRFToken(),
    ])
    expect(session?.user.displayName).toBe('Logto Owner')
    expect(csrf).toBe('csrf-secret')
    expect(fetch).toHaveBeenCalledTimes(1)
    expect(fetch).toHaveBeenCalledWith('/api/auth/session', {
      credentials: 'include',
      cache: 'no-store',
    })
  })

  it('treats a missing cookie session as signed out', async () => {
    vi.mocked(fetch).mockResolvedValueOnce({
      ok: false,
      status: 401,
    } as Response)
    const { getBrowserSession, getCSRFToken } = await import('./logto')
    expect(await getBrowserSession()).toBeNull()
    expect(await getCSRFToken()).toBeUndefined()
  })

  it('can retry a transient backend failure', async () => {
    vi.mocked(fetch).mockRejectedValueOnce(new Error('offline'))
    const { getBrowserSession } = await import('./logto')
    await expect(getBrowserSession()).rejects.toThrow('offline')
    await expect(getBrowserSession()).resolves.toEqual(browserSession)
  })

  it('starts login at the backend with a confined return path', async () => {
    const { signIn } = await import('./logto')
    await signIn('/connections?provider=nocodb')
    expect(window.location.assign).toHaveBeenCalledWith(
      '/api/auth/login?redirect=%2Fconnections%3Fprovider%3Dnocodb'
    )
    await signIn('//evil.test', true)
    expect(window.location.assign).toHaveBeenLastCalledWith(
      '/api/auth/login?redirect=%2F&reauth=1'
    )
    expect(fetch).not.toHaveBeenCalled()
  })

  it('logs out through a top-level CSRF-protected POST without receiving Logto tokens', async () => {
    const form = { method: '', action: '', append: vi.fn(), submit: vi.fn() }
    const input = { type: '', name: '', value: '' }
    vi.stubGlobal('document', {
      createElement: (tag: string) => (tag === 'form' ? form : input),
      body: { append: vi.fn() },
    })
    const { signOut } = await import('./logto')
    await signOut()
    expect(form.method).toBe('post')
    expect(form.action).toBe('/api/auth/logout')
    expect(input.name).toBe('csrf_token')
    expect(input.value).toBe('csrf-secret')
    expect(form.submit).toHaveBeenCalledOnce()
  })
})

it('confines post-login navigation to resource links', async () => {
  const { safeReturnPath } = await import('./logto')
  expect(safeReturnPath('/connections?provider=nocodb#list')).toBe(
    '/connections?provider=nocodb#list'
  )
  for (const target of [
    undefined,
    'https://evil.test',
    '//evil.test',
    '/\\evil.test',
    '/\n/evil.test',
    '/sign-in',
    '/auth/callback?code=old',
    '/api/auth/logout',
    '/connections/../sign-in',
  ]) {
    expect(safeReturnPath(target)).toBe('/')
  }
})
