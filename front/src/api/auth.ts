import { AuthService } from '@/gen/neobox/v1/auth_pb'
import { makeClient } from './transport'

export interface AuthUser {
  id: string
  username: string
  displayName?: string
  display_name?: string
  avatarUrl?: string
  avatar_url?: string
  role?: string
}

const client = makeClient(AuthService)

export async function me(): Promise<{ user?: AuthUser }> {
  const response = await client.me({})
  return { user: response.user }
}
