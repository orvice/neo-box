import { useState } from 'react'

export const GROUP_SCOPE =
  'https://www.googleapis.com/auth/admin.directory.group'
export const ORG_SCOPE =
  'https://www.googleapis.com/auth/admin.directory.orgunit'
export const SECURITY_SCOPE =
  'https://www.googleapis.com/auth/admin.directory.user.security'
export const GMAIL_SCOPE =
  'https://www.googleapis.com/auth/gmail.settings.basic'
export function useCursor() {
  const [tokens, setTokens] = useState<string[]>([''])
  return {
    token: tokens[tokens.length - 1],
    page: tokens.length,
    reset: () => setTokens(['']),
    previous: () => setTokens((v) => v.slice(0, -1)),
    next: (token: string) => setTokens((v) => [...v, token]),
  }
}
