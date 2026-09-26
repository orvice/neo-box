import { Cloud, DatabaseBackup, type LucideIcon } from 'lucide-react'
import { Provider } from '@/api/connections'

/**
 * What the app shell (sidebar, command menu) needs to know about a provider.
 * Pages and forms live in ./providers so they stay in their route chunks.
 */
export type ProviderInfo = {
  provider: Provider
  /** Key in URLs, e.g. `/connections?provider=nocodb`. */
  key: string
  label: string
  description: string
  icon: LucideIcon
}

export const providerInfos: ProviderInfo[] = [
  {
    provider: Provider.NOCODB,
    key: 'nocodb',
    label: 'NocoDB',
    description:
      'Snapshot the Bases of a self-hosted NocoDB, on demand or on a schedule.',
    icon: DatabaseBackup,
  },
  {
    provider: Provider.WASABI,
    key: 'wasabi',
    label: 'Wasabi',
    description:
      'Daily storage, egress, and an estimated cost for a Wasabi account.',
    icon: Cloud,
  },
]

export function providerInfoByKey(key: string | undefined) {
  return providerInfos.find((p) => p.key === key)
}
