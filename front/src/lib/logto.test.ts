import { describe, expect, it, vi, beforeEach } from 'vitest'

const sdk = vi.hoisted(() => ({
  isAuthenticated: vi.fn(),
  getAccessToken: vi.fn(),
  signIn: vi.fn(),
  signOut: vi.fn(),
}))
vi.mock('@logto/browser', () => ({
  default: class {
    constructor() {
      return sdk
    }
  },
}))

describe('Logto API token', () => {
  beforeEach(() => {
    vi.resetModules()
    vi.clearAllMocks()
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue({
        ok: true,
        json: async () => ({
          endpoint: 'https://identity.test',
          appId: 'spa',
          resource: 'https://api.test',
          scopes: ['access'],
        }),
      })
    )
  })

  it('requests a resource access token for each request so the SDK can refresh it', async () => {
    sdk.isAuthenticated.mockResolvedValue(true)
    sdk.getAccessToken
      .mockResolvedValueOnce('initial')
      .mockResolvedValueOnce('refreshed')
    const { getAccessToken } = await import('./logto')
    expect(await getAccessToken()).toBe('initial')
    expect(await getAccessToken()).toBe('refreshed')
    expect(sdk.getAccessToken).toHaveBeenNthCalledWith(1, 'https://api.test')
    expect(sdk.getAccessToken).toHaveBeenNthCalledWith(2, 'https://api.test')
    expect(fetch).toHaveBeenCalledTimes(1)
  })

  it('does not issue an API token when signed out', async () => {
    sdk.isAuthenticated.mockResolvedValue(false)
    const { getAccessToken } = await import('./logto')
    expect(await getAccessToken()).toBeUndefined()
    expect(sdk.getAccessToken).not.toHaveBeenCalled()
  })

  it('can retry loading configuration after a transient failure', async () => {
    vi.mocked(fetch).mockRejectedValueOnce(new Error('offline'))
    const { getLogto } = await import('./logto')
    await expect(getLogto()).rejects.toThrow('offline')
    await expect(getLogto()).resolves.toHaveProperty('client')
  })
})

describe('post sign-in redirect', () => {
  it('keeps resource links and rejects external targets and auth loops', async () => {
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
      '/connections/../sign-in',
    ]) {
      expect(safeReturnPath(target)).toBe('/')
    }
  })
})
