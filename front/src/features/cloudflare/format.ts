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
    scope: 'Account',
    permission: 'Workers Scripts · Edit',
    unlocks:
      'managing Workers: workers.dev, custom domains, cron triggers, secrets, rollback, delete',
  },
  {
    scope: 'Zone',
    permission: 'Workers Routes · Read',
    unlocks: 'Worker routes',
  },
  {
    scope: 'Zone',
    permission: 'Workers Routes · Edit',
    unlocks: 'adding and deleting Worker routes',
  },
  {
    scope: 'Account',
    permission: 'Cloudflare Pages · Read',
    unlocks: 'Pages tab',
  },
  {
    scope: 'Account',
    permission: 'Cloudflare Pages · Edit',
    unlocks:
      'managing Pages projects: settings, variables, deployments, domains, delete',
  },
  {
    scope: 'Account',
    permission: 'Cloudflare Tunnel · Read',
    unlocks: 'Tunnels tab',
  },
  {
    scope: 'Account',
    permission: 'Cloudflare Tunnel · Edit',
    unlocks: 'creating Tunnels and changing their hostnames',
  },
  {
    scope: 'Zone',
    permission: 'Email Routing Rules · Read',
    unlocks: 'the Email routing tab',
  },
  {
    scope: 'Zone',
    permission: 'Email Routing Rules · Edit',
    unlocks: 'enabling Email Routing, and changing rules and the catch-all',
  },
  {
    scope: 'Account',
    permission: 'Email Routing Addresses · Read',
    unlocks: 'destination addresses',
  },
  {
    scope: 'Account',
    permission: 'Email Routing Addresses · Edit',
    unlocks: 'adding and deleting destination addresses',
  },
]
