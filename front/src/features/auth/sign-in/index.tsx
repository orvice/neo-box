import { useEffect, useState } from 'react'
import { useNavigate, useSearch } from '@tanstack/react-router'
import { useAuthStore } from '@/stores/auth-store'
import { safeReturnPath } from '@/lib/logto'
import { Button } from '@/components/ui/button'
import { AuthLayout } from '../auth-layout'

export function SignIn() {
  const { redirect } = useSearch({ from: '/(auth)/sign-in' })
  const navigate = useNavigate()
  const auth = useAuthStore((state) => state.auth)
  const restore = auth.restore
  const [pending, setPending] = useState(false)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    void restore()
  }, [restore])
  useEffect(() => {
    if (auth.authenticated)
      void navigate({ href: safeReturnPath(redirect), replace: true })
  }, [auth.authenticated, navigate, redirect])

  async function login() {
    setPending(true)
    setError(null)
    try {
      await auth.login(redirect)
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Unable to sign in.')
      setPending(false)
    }
  }

  async function changeAccount() {
    setPending(true)
    try {
      await auth.logout()
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Unable to sign out.')
      setPending(false)
    }
  }

  return (
    <AuthLayout>
      <section aria-labelledby='sign-in-title'>
        <div className='mb-8'>
          <p className='mb-2 text-sm font-medium text-[#2b658b] dark:text-[#82c4ea]'>
            Account access
          </p>
          <h1
            id='sign-in-title'
            className='font-manrope text-2xl leading-tight font-semibold text-foreground'
          >
            Sign in to Neo Box
          </h1>
          <p className='mt-2 text-sm leading-6 text-muted-foreground'>
            Continue with your Logto account.
          </p>
        </div>
        {(error || auth.error) && (
          <p role='alert' className='mb-4 text-sm text-destructive'>
            {error || auth.error}
          </p>
        )}
        <Button
          className='w-full'
          disabled={pending || auth.restoring}
          onClick={() => void login()}
        >
          {pending ? 'Redirecting…' : 'Continue with Logto'}
        </Button>
        {auth.error && (
          <Button
            className='mt-3 w-full'
            variant='outline'
            disabled={pending}
            onClick={() => void changeAccount()}
          >
            Use another account
          </Button>
        )}
      </section>
    </AuthLayout>
  )
}
