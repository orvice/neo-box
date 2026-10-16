import { useState, type FormEvent } from 'react'
import { getRouteApi, Link } from '@tanstack/react-router'
import { toast } from 'sonner'
import { ConnectionStatus, type Connection } from '@/api/connections'
import {
  useWorkspaceTenants,
  useWorkspaceTenantChanges,
  type WorkspaceTenant,
} from '@/api/google-workspace'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { Page, PageHeader, PageScroll } from '@/components/common/page-parts'
import { ConfirmDialog } from '@/components/confirm-dialog'
import { DataTable, type Column } from '@/components/data-table'
import { WorkspaceOperations } from './operations'
import { WorkspaceOverview } from './overview'
import { WorkspaceTenantDetail } from './tenant-detail'

const route = getRouteApi('/_authenticated/connections/$connectionId/')

export function WorkspaceConnectionDetail({
  connection,
}: {
  connection: Connection
}) {
  const tenants = useWorkspaceTenants(connection.id)
  const changes = useWorkspaceTenantChanges(connection.id)
  // A user page links back with the tenant it came from.
  const [selectedId, setSelectedId] = useState(route.useSearch().tenant ?? '')
  const [form, setForm] = useState<WorkspaceTenant | 'new' | null>(null)
  const [remove, setRemove] = useState<WorkspaceTenant | null>(null)
  const [error, setError] = useState('')
  const config =
    connection.config.case === 'googleWorkspace'
      ? connection.config.value
      : undefined
  const selected = tenants.data?.find((t) => t.id === selectedId)
  const columns: Column<WorkspaceTenant>[] = [
    {
      header: 'Tenant',
      cell: (t) => (
        <Button
          variant='link'
          className='px-0'
          onClick={() => setSelectedId(t.id)}
        >
          {t.name}
        </Button>
      ),
    },
    { header: 'Customer ID', accessorKey: 'customerId' },
    { header: 'Delegated administrator', accessorKey: 'delegatedAdminEmail' },
    {
      header: 'Last authorization check',
      cell: (t) => (
        <span title={t.statusMessage}>
          {t.status === ConnectionStatus.OK
            ? 'OK'
            : t.status === ConnectionStatus.ERROR
              ? `Error: ${t.statusMessage}`
              : 'Not checked'}
        </span>
      ),
    },
    {
      header: 'Actions',
      cell: (t) => (
        <div className='flex gap-2'>
          <Button
            size='sm'
            variant='outline'
            disabled={changes.test.isPending}
            onClick={async () => {
              setError('')
              try {
                const r = await changes.test.mutateAsync(t.id)
                if (r.tenant?.status === ConnectionStatus.ERROR)
                  setError(r.tenant.statusMessage)
                else toast.success('Tenant authorization verified')
              } catch (e) {
                setError(String(e))
              }
            }}
          >
            Test
          </Button>
          <Button size='sm' variant='outline' onClick={() => setForm(t)}>
            Edit
          </Button>
          <Button
            size='sm'
            variant='outline'
            onClick={() => {
              setError('')
              setRemove(t)
            }}
          >
            Remove
          </Button>
        </div>
      ),
    },
  ]
  return (
    <Page>
      <PageHeader
        title={connection.name}
        breadcrumb={
          <Link to='/connections' search={{ provider: 'google_workspace' }}>
            Google Workspace connections
          </Link>
        }
        subtitle={
          <span>
            {config?.serviceAccountEmail} · Client ID {config?.clientId}
          </span>
        }
        actions={
          <Button onClick={() => setForm('new')}>Add Workspace tenant</Button>
        }
      />
      <PageScroll className='space-y-6'>
        <Alert>
          <AlertTitle>
            Authorize the same Service Account in every tenant
          </AlertTitle>
          <AlertDescription>
            <p>
              In each tenant's Admin console, a super administrator must add
              Client ID <strong>{config?.clientId}</strong> under Security → API
              controls → Domain-wide delegation. Grant these scopes:
            </p>
            <p className='mt-2 font-mono text-xs break-all'>
              https://www.googleapis.com/auth/admin.directory.user,
              <br />
              https://www.googleapis.com/auth/admin.directory.domain.readonly
            </p>
            <p className='mt-2'>
              Use an administrator from that tenant with permission to manage
              users. Saving a tenant verifies a read; Google checks each write
              against the administrator's privileges.
            </p>
            <a
              href='https://developers.google.com/identity/protocols/oauth2/service-account#delegatingauthority'
              target='_blank'
              rel='noreferrer'
              className='underline'
            >
              Google setup instructions
            </a>
            <p className='mt-2'>
              Optional features need these additional scopes in each tenant.
              They are requested only when using the feature:
            </p>
            <ul className='mt-1 space-y-1 text-xs break-all'>
              <li>
                Groups and members:{' '}
                <code>
                  https://www.googleapis.com/auth/admin.directory.group
                </code>
              </li>
              <li>
                Organization units:{' '}
                <code>
                  https://www.googleapis.com/auth/admin.directory.orgunit
                </code>
              </li>
              <li>
                Google audit:{' '}
                <code>
                  https://www.googleapis.com/auth/admin.reports.audit.readonly
                </code>
              </li>
              <li>
                Sign-out and application grants:{' '}
                <code>
                  https://www.googleapis.com/auth/admin.directory.user.security
                </code>
              </li>
            </ul>
          </AlertDescription>
        </Alert>
        {(error || tenants.error) && (
          <Alert variant='destructive'>
            <AlertDescription>
              {error || String(tenants.error)}
            </AlertDescription>
          </Alert>
        )}
        <Tabs defaultValue='tenants'>
          <TabsList>
            <TabsTrigger value='tenants'>Tenants</TabsTrigger>
            <TabsTrigger value='overview'>Overview</TabsTrigger>
            <TabsTrigger value='operations'>Neo Box operations</TabsTrigger>
          </TabsList>
          <TabsContent value='tenants' className='space-y-6 pt-4'>
            {' '}
            {!tenants.error && (
              <DataTable
                columns={columns}
                data={tenants.data}
                isLoading={tenants.isLoading}
                emptyMessage='No Workspace tenants yet'
                emptyDescription='Add a Customer ID and delegated administrator for each independent organization.'
              />
            )}
            {selected && (
              <WorkspaceTenantDetail
                key={selected.id}
                connectionId={connection.id}
                tenant={selected}
              />
            )}
          </TabsContent>
          <TabsContent value='overview' className='pt-4'>
            <WorkspaceOverview connectionId={connection.id} />
          </TabsContent>
          <TabsContent value='operations' className='pt-4'>
            <WorkspaceOperations connectionId={connection.id} />
          </TabsContent>
        </Tabs>
        {form && (
          <Dialog
            open
            onOpenChange={(open) => {
              if (!open) setForm(null)
            }}
          >
            <DialogContent>
              <DialogHeader>
                <DialogTitle>
                  {form === 'new'
                    ? 'Add Workspace tenant'
                    : 'Edit Workspace tenant'}
                </DialogTitle>
              </DialogHeader>
              <TenantForm
                connectionId={connection.id}
                tenant={form === 'new' ? undefined : form}
                onDone={() => setForm(null)}
              />
            </DialogContent>
          </Dialog>
        )}
        <ConfirmDialog
          open={!!remove}
          onOpenChange={(open) => {
            if (!open) setRemove(null)
          }}
          title={`Remove ${remove?.name}?`}
          desc='This removes the tenant configuration from Neo Box. Google users and delegated authorization remain unchanged.'
          confirmText='Remove tenant'
          destructive
          isLoading={changes.remove.isPending}
          handleConfirm={async () => {
            if (!remove) return
            try {
              await changes.remove.mutateAsync(remove.id)
              if (selectedId === remove.id) setSelectedId('')
              setRemove(null)
              toast.success('Tenant removed')
            } catch (e) {
              setError(String(e))
            }
          }}
        />
      </PageScroll>
    </Page>
  )
}
function TenantForm({
  connectionId,
  tenant,
  onDone,
}: {
  connectionId: string
  tenant?: WorkspaceTenant
  onDone: () => void
}) {
  const changes = useWorkspaceTenantChanges(connectionId)
  const [name, setName] = useState(tenant?.name ?? '')
  const [customerId, setCustomerId] = useState(tenant?.customerId ?? '')
  const [admin, setAdmin] = useState(tenant?.delegatedAdminEmail ?? '')
  const [error, setError] = useState('')
  const pending = changes.create.isPending || changes.update.isPending
  async function submit(e: FormEvent) {
    e.preventDefault()
    setError('')
    try {
      const fields = { name: name.trim(), delegatedAdminEmail: admin.trim() }
      if (tenant)
        await changes.update.mutateAsync({ tenantId: tenant.id, ...fields })
      else
        await changes.create.mutateAsync({
          customerId: customerId.trim(),
          ...fields,
        })
      onDone()
    } catch (e) {
      setError(String(e))
    }
  }
  return (
    <form onSubmit={submit} className='space-y-4'>
      <div className='space-y-2'>
        <Label htmlFor='tenant-name'>Name</Label>
        <Input
          id='tenant-name'
          value={name}
          onChange={(e) => setName(e.target.value)}
          required
        />
      </div>
      <div className='space-y-2'>
        <Label htmlFor='tenant-customer'>Customer ID</Label>
        <Input
          id='tenant-customer'
          value={customerId}
          onChange={(e) => setCustomerId(e.target.value)}
          disabled={!!tenant}
          required
          placeholder='C01234567'
        />
        <p className='text-xs text-muted-foreground'>
          Admin console → Account → Account settings → Profile. Use the Customer
          ID, not the domain name.
        </p>
      </div>
      <div className='space-y-2'>
        <Label htmlFor='tenant-admin'>Delegated administrator email</Label>
        <Input
          id='tenant-admin'
          value={admin}
          onChange={(e) => setAdmin(e.target.value)}
          type='email'
          required
          autoComplete='off'
        />
      </div>
      {error && (
        <p role='alert' className='text-sm text-destructive'>
          {error}
        </p>
      )}
      <div className='flex justify-end gap-2'>
        <Button
          type='button'
          variant='outline'
          disabled={pending}
          onClick={onDone}
        >
          Cancel
        </Button>
        <Button disabled={pending}>
          {pending ? 'Verifying…' : 'Save tenant'}
        </Button>
      </div>
    </form>
  )
}
