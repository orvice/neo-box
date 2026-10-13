import type { WorkspaceTenant } from '@/api/google-workspace'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { WorkspaceAudit } from './audit'
import { WorkspaceDrive } from './drive'
import { WorkspaceGroups } from './groups'
import { WorkspaceOperations } from './operations'
import { WorkspaceOrgUnits } from './org-units'
import { WorkspaceOverview } from './overview'
import { WorkspaceUsers } from './users'

export function WorkspaceTenantDetail({
  connectionId,
  tenant,
}: {
  connectionId: string
  tenant: WorkspaceTenant
}) {
  return (
    <section className='space-y-4'>
      <div>
        <h2 className='text-xl font-semibold'>{tenant.name}</h2>
        <p className='text-sm text-muted-foreground'>
          {tenant.customerId} · {tenant.delegatedAdminEmail}
        </p>
      </div>
      <Tabs defaultValue='users'>
        <TabsList className='h-auto flex-wrap'>
          <TabsTrigger value='users'>Users</TabsTrigger>
          <TabsTrigger value='groups'>Groups</TabsTrigger>
          <TabsTrigger value='org-units'>Organization units</TabsTrigger>
          <TabsTrigger value='security'>Account security</TabsTrigger>
          <TabsTrigger value='drive'>Drive</TabsTrigger>
          <TabsTrigger value='operations'>Neo Box operations</TabsTrigger>
          <TabsTrigger value='audit'>Google audit</TabsTrigger>
        </TabsList>
        <TabsContent value='users' className='pt-4'>
          <WorkspaceUsers connectionId={connectionId} tenant={tenant} />
        </TabsContent>
        <TabsContent value='groups' className='pt-4'>
          <WorkspaceGroups connectionId={connectionId} tenantId={tenant.id} />
        </TabsContent>
        <TabsContent value='org-units' className='pt-4'>
          <WorkspaceOrgUnits connectionId={connectionId} tenantId={tenant.id} />
        </TabsContent>
        <TabsContent value='security' className='pt-4'>
          <WorkspaceOverview
            connectionId={connectionId}
            tenantId={tenant.id}
            securityOnly
          />
        </TabsContent>
        <TabsContent value='drive' className='pt-4'>
          <WorkspaceDrive connectionId={connectionId} tenantId={tenant.id} />
        </TabsContent>
        <TabsContent value='operations' className='pt-4'>
          <WorkspaceOperations
            connectionId={connectionId}
            tenantId={tenant.id}
          />
        </TabsContent>
        <TabsContent value='audit' className='pt-4'>
          <WorkspaceAudit connectionId={connectionId} tenantId={tenant.id} />
        </TabsContent>
      </Tabs>
    </section>
  )
}
