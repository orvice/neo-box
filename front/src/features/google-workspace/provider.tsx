import type { ProviderViews } from '@/features/connections/types'
import { WorkspaceConnectionDetail } from './connection-detail'
import { WorkspaceConnectionForm } from './connection-form'

export const workspaceViews: ProviderViews = {
  formHint:
    'One Service Account reused across multiple independent Google Workspace tenants. Enable the Admin SDK API in its Google Cloud project.',
  deleteWarning:
    'Its tenant configurations in Neo Box will be removed. Google users and Google authorization remain unchanged.',
  Form: WorkspaceConnectionForm,
  Summary: ({ connection }) => (
    <span className='text-sm break-all text-muted-foreground'>
      {connection.config.case === 'googleWorkspace'
        ? connection.config.value.serviceAccountEmail
        : ''}
    </span>
  ),
  Detail: WorkspaceConnectionDetail,
}
