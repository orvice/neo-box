import type { ProviderViews } from '@/features/connections/types'
import { CloudflareConnectionDetail } from './connection-detail'
import { CloudflareConnectionForm } from './connection-form'
import { CloudflareConnectionSummary } from './connection-summary'

export const cloudflareViews: ProviderViews = {
  formHint:
    'One Cloudflare account. The token is checked against the account before saving (nothing is changed) and stored encrypted; it only needs the permissions for what you want to see or edit.',
  deleteWarning:
    'Its DNS operation log in Neo Box will be deleted. Nothing changes in Cloudflare.',
  Form: CloudflareConnectionForm,
  Summary: CloudflareConnectionSummary,
  Detail: CloudflareConnectionDetail,
}
