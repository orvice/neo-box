import { create } from 'zustand'
import { me, type AuthUser } from '@/api/auth'
import { getLogto, signIn, signOut } from '@/lib/logto'

interface AuthState {
  auth: {
    user: AuthUser | null
    authenticated: boolean
    restoring: boolean
    checked: boolean
    error: string | null
    restore: () => Promise<void>
    login: (redirectTo?: string) => Promise<void>
    logout: () => Promise<void>
    reset: () => void
  }
}

let restoration: Promise<void> | undefined

export const useAuthStore = create<AuthState>()((set, get) => ({
  auth: {
    user: null,
    authenticated: false,
    restoring: false,
    checked: false,
    error: null,
    restore: async () => {
      if (restoration) return restoration
      if (get().auth.checked) return
      restoration = (async () => {
        set((state) => ({
          auth: { ...state.auth, restoring: true, error: null },
        }))
        try {
          const { client } = await getLogto()
          if (!(await client.isAuthenticated())) return
          // Me verifies API access and resolves the stable business owner ID.
          const response = await me()
          if (!response.user) throw new Error('Unable to load your account.')
          const claims = await client.getIdTokenClaims()
          const user: AuthUser = {
            ...response.user,
            username: claims.username || claims.email || claims.sub,
            displayName:
              claims.name || claims.username || claims.email || claims.sub,
            avatarUrl: claims.picture || '',
          }
          set((state) => ({
            auth: { ...state.auth, user, authenticated: true },
          }))
        } catch (error) {
          set((state) => ({
            auth: {
              ...state.auth,
              user: null,
              authenticated: false,
              error:
                error instanceof Error
                  ? error.message
                  : 'Unable to restore sign-in.',
            },
          }))
        } finally {
          set((state) => ({
            auth: { ...state.auth, checked: true, restoring: false },
          }))
        }
      })().finally(() => {
        restoration = undefined
      })
      return restoration
    },
    login: signIn,
    logout: async () => {
      await signOut()
      get().auth.reset()
    },
    reset: () =>
      set((state) => ({
        auth: {
          ...state.auth,
          user: null,
          authenticated: false,
          checked: false,
          error: null,
        },
      })),
  },
}))
