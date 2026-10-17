import { useEffect, useState } from 'react'
import { Link } from '@tanstack/react-router'
import { completeSignIn } from '@/lib/logto'
import { AuthLayout } from './auth-layout'

// Share callback processing across StrictMode mounts. The authorization code
// is single-use and must never be exchanged twice.
let completion: Promise<string> | undefined

export function LogtoCallback() {
  const [error, setError] = useState<string | null>(null)
  useEffect(() => {
    let active = true
    completion ??= completeSignIn()
    void completion
      .then((path) => {
        if (active) window.location.replace(path)
      })
      .catch((e: unknown) => {
        if (active) setError(e instanceof Error ? e.message : 'Sign-in failed.')
      })
    return () => {
      active = false
    }
  }, [])

  return (
    <AuthLayout>
      {error ? (
        <div>
          <p role='alert' className='mb-4 text-sm text-destructive'>
            {error}
          </p>
          <Link to='/sign-in' className='text-sm underline'>
            Return to sign in
          </Link>
        </div>
      ) : (
        <p role='status'>Completing sign-in…</p>
      )}
    </AuthLayout>
  )
}
