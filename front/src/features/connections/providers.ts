import { Provider } from '@/api/connections'
import { nocodbViews } from '@/features/nocodb/provider'
import { providerInfos, type ProviderInfo } from './provider-info'
import type { ProviderViews } from './types'

export type ProviderDef = ProviderInfo & ProviderViews

const views: Partial<Record<Provider, ProviderViews>> = {
  [Provider.NOCODB]: nocodbViews,
}

/** Every provider the dashboard can show, in sidebar order. */
export const providers: ProviderDef[] = providerInfos.flatMap((info) => {
  const v = views[info.provider]
  return v ? [{ ...info, ...v }] : []
})

export function providerOf(p: Provider) {
  return providers.find((d) => d.provider === p)
}

export function providerByKey(key: string | undefined) {
  return providers.find((d) => d.key === key)
}
