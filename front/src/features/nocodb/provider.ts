import type { ProviderViews } from '@/features/connections/types'
import { NocoDBConnectionDetail } from './connection-detail'
import { NocoDBConnectionForm } from './connection-form'
import { NocoDBConnectionSummary } from './connection-summary'

export const nocodbViews: ProviderViews = {
  formHint:
    'Create an API token in NocoDB under Team & Settings → Tokens. It is verified before saving and stored encrypted.',
  deleteWarning:
    'All of its snapshots and schedules will be permanently deleted. Data in NocoDB is not touched.',
  Form: NocoDBConnectionForm,
  Summary: NocoDBConnectionSummary,
  Detail: NocoDBConnectionDetail,
}
