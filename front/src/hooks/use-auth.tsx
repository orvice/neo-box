import { useAuthStore } from '@/stores/auth-store'

export function useAuth() {
  const auth = useAuthStore((state) => state.auth)
  return {
    user: auth.user,
    isAuthenticated: auth.authenticated,
    isLoading: auth.restoring,
    login: auth.login,
    logout: auth.logout,
  }
}
