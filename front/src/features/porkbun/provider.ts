import type { ProviderViews } from '@/features/connections/types'
import { PorkbunConnectionDetail } from './connection-detail'
import { PorkbunConnectionForm } from './connection-form'
import { PorkbunConnectionSummary } from './connection-summary'

export const porkbunViews: ProviderViews = {
  formHint:
    'One Porkbun account. The API key pair is checked with /ping before saving (nothing is changed) and the secret key is stored encrypted. Neo Box only performs read-only queries.',
  deleteWarning:
    'This connection will be removed from Neo Box. Nothing changes in Porkbun.',
  Form: PorkbunConnectionForm,
  Summary: PorkbunConnectionSummary,
  Detail: PorkbunConnectionDetail,
}
