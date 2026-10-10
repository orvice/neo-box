import { type Connection } from '@/api/connections'

/** The Cloudflare settings of a connection, if it is one. */
export function cloudflareConfig(connection: Connection | undefined) {
  return connection?.config.case === 'cloudflare'
    ? connection.config.value
    : undefined
}

export const SETUP_GUIDE = 'https://github.com/orvice/neo-box#cloudflare'
export const ACCOUNT_ID_DOCS =
  'https://developers.cloudflare.com/fundamentals/account/find-account-and-zone-ids/'
export const CREATE_TOKEN_DOCS =
  'https://developers.cloudflare.com/fundamentals/api/get-started/create-token/'

/** Token permissions, by what they unlock in Neo Box. */
export const TOKEN_PERMISSIONS: {
  scope: string
  permission: string
  unlocks: string
}[] = [
  { scope: 'Zone', permission: 'Zone · Read', unlocks: 'Zones tab' },
  { scope: 'Zone', permission: 'DNS · Read', unlocks: 'DNS records' },
  { scope: 'Zone', permission: 'DNS · Edit', unlocks: 'changing DNS records' },
  {
    scope: 'Account',
    permission: 'Workers Scripts · Read',
    unlocks: 'Workers tab',
  },
  {
    scope: 'Zone',
    permission: 'Workers Routes · Read',
    unlocks: 'Worker routes',
  },
  {
    scope: 'Account',
    permission: 'Cloudflare Pages · Read',
    unlocks: 'Pages tab',
  },
]
