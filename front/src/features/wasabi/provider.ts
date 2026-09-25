import type { ProviderViews } from '@/features/connections/types'
import { WasabiConnectionDetail } from './connection-detail'
import { WasabiConnectionForm } from './connection-form'
import { WasabiConnectionSummary } from './connection-summary'

export const wasabiViews: ProviderViews = {
  formHint:
    'Stats access for one Wasabi account. The key is checked with a Stats API call before saving and stored encrypted; the last 12 months of usage are synced right after.',
  deleteWarning:
    'Its synced usage history will be deleted. Nothing changes in Wasabi.',
  Form: WasabiConnectionForm,
  Summary: WasabiConnectionSummary,
  Detail: WasabiConnectionDetail,
}
