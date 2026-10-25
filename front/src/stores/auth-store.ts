import { create } from 'zustand'
import type { AuthUser } from '@/api/auth'
import {
  getBrowserSession,
  clearBrowserSession,
  signIn,
  signOut,
} from '@/lib/logto'

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
          const session = await getBrowserSession()
          if (!session) return
          const user = session.user
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
    reset: () => {
      clearBrowserSession()
      set((state) => ({
        auth: {
          ...state.auth,
          user: null,
          authenticated: false,
          checked: false,
          error: null,
        },
      }))
    },
  },
}))
