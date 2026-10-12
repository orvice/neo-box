import { useState } from 'react'
import { Link } from '@tanstack/react-router'
import { useConnections, Provider } from '@/api/connections'
import { Label } from '@/components/ui/label'
import { Page, PageHeader, PageScroll } from '@/components/common/page-parts'
import { WorkspaceOverview } from './overview'

export function WorkspaceOverviewPage() {
  const connections = useConnections(Provider.GOOGLE_WORKSPACE)
  const [connectionId, setConnectionId] = useState('')
  return (
    <Page>
      <PageHeader
        title='Google Workspace overview'
        subtitle='All your Workspace tenants across Service Accounts.'
        breadcrumb={
          <Link to='/connections' search={{ provider: 'google_workspace' }}>
            Google Workspace connections
          </Link>
        }
      />
      <PageScroll>
        <div className='mb-5 max-w-sm space-y-2'>
          <Label htmlFor='overview-service-account'>Service Account</Label>
          <select
            id='overview-service-account'
            className='h-9 w-full rounded-md border bg-background px-3 text-sm'
            value={connectionId}
            onChange={(e) => setConnectionId(e.target.value)}
          >
            <option value=''>All Service Accounts</option>
            {connections.data?.map((c) => (
              <option key={c.id} value={c.id}>
                {c.name}
              </option>
            ))}
          </select>
        </div>
        <WorkspaceOverview key={connectionId} connectionId={connectionId} />
      </PageScroll>
    </Page>
  )
}
