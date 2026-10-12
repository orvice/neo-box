import { useState, type FormEvent } from 'react'
import { toast } from 'sonner'
import {
  useWorkspaceUsers,
  useWorkspaceUser,
  useWorkspaceUserChanges,
  type WorkspaceTenant,
  type WorkspaceUser,
} from '@/api/google-workspace'
import { Alert, AlertDescription } from '@/components/ui/alert'
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
import { ConfirmDialog } from '@/components/confirm-dialog'
import { DataTable, type Column } from '@/components/data-table'
import { PasswordInput } from '@/components/password-input'
import { triState, lastLoginText } from './observability-format'
import { OrgUnitPicker } from './org-units'
import { UserAliases, UserGroups, UserSecurity } from './user-tools'

type UserAction =
  { kind: 'edit' | 'password'; user: WorkspaceUser } | { kind: 'create' }
export function WorkspaceUsers({
  connectionId,
  tenant,
}: {
  connectionId: string
  tenant: WorkspaceTenant
}) {
  const [query, setQuery] = useState('')
  const [search, setSearch] = useState('')
  const [tokens, setTokens] = useState<string[]>([''])
  const [action, setAction] = useState<UserAction | null>(null)
  const [suspend, setSuspend] = useState<WorkspaceUser | null>(null)
  const [detail, setDetail] = useState<WorkspaceUser | null>(null)
  const [error, setError] = useState('')
  const users = useWorkspaceUsers(
    connectionId,
    tenant.id,
    query,
    tokens[tokens.length - 1]
  )
  const changes = useWorkspaceUserChanges(connectionId, tenant.id)
  const columns: Column<WorkspaceUser>[] = [
    {
      header: 'Email',
      cell: (u) => (
        <Button variant='link' className='px-0' onClick={() => setDetail(u)}>
          {u.primaryEmail}
        </Button>
      ),
    },
    { header: 'Name', cell: (u) => `${u.givenName} ${u.familyName}` },
    { header: 'Organization unit', accessorKey: 'orgUnitPath' },
    {
      header: 'Status',
      cell: (u) =>
        `${u.suspended ? 'Suspended' : 'Active'}${u.isAdmin ? ' · Admin' : ''}`,
    },
    {
      header: 'Actions',
      cell: (u) => (
        <div className='flex flex-wrap gap-2'>
          <Button
            size='sm'
            variant='outline'
            onClick={() => setAction({ kind: 'edit', user: u })}
          >
            Edit
          </Button>
          <Button
            size='sm'
            variant='outline'
            onClick={() => setAction({ kind: 'password', user: u })}
          >
            Reset password
          </Button>
          <Button
            size='sm'
            variant='outline'
            onClick={() => {
              setError('')
              setSuspend(u)
            }}
          >
            {u.suspended ? 'Restore' : 'Suspend'}
          </Button>
        </div>
      ),
    },
  ]
  return (
    <section className='space-y-4'>
      <div className='flex flex-wrap items-center justify-between gap-3'>
        <div>
          <h2 className='text-xl font-semibold'>Users</h2>
          <p className='text-sm text-muted-foreground'>
            {tenant.customerId} · read live from Google
          </p>
        </div>
        <div className='flex gap-2'>
          <Button
            variant='outline'
            disabled={users.isFetching}
            onClick={() => void users.refetch()}
          >
            Refresh
          </Button>
          <Button onClick={() => setAction({ kind: 'create' })}>
            Create user
          </Button>
        </div>
      </div>
      <form
        className='flex gap-2'
        onSubmit={(e) => {
          e.preventDefault()
          setQuery(search.trim())
          setTokens([''])
        }}
      >
        <Input
          aria-label='Google Directory user query'
          placeholder='Directory query, e.g. email:alex*'
          value={search}
          onChange={(e) => setSearch(e.target.value)}
        />
        <Button variant='outline'>Search</Button>
      </form>
      {users.error ? (
        <Alert variant='destructive'>
          <AlertDescription>{String(users.error)}</AlertDescription>
        </Alert>
      ) : (
        <DataTable
          columns={columns}
          data={users.data?.users}
          isLoading={users.isLoading}
          emptyMessage={
            query ? 'No users match this query' : 'No users in this tenant'
          }
        />
      )}
      <div className='flex items-center justify-end gap-3'>
        <span className='text-sm text-muted-foreground'>
          Page {tokens.length}
        </span>
        <Button
          variant='outline'
          disabled={tokens.length < 2 || users.isFetching}
          onClick={() => setTokens((v) => v.slice(0, -1))}
        >
          Previous
        </Button>
        <Button
          variant='outline'
          disabled={
            !users.data?.nextPageToken || users.isFetching || !!users.error
          }
          onClick={() => {
            if (users.data?.nextPageToken)
              setTokens((v) => [...v, users.data.nextPageToken])
          }}
        >
          Next
        </Button>
      </div>
      {action && (
        <Dialog
          open
          onOpenChange={(open) => {
            if (!open) setAction(null)
          }}
        >
          <DialogContent>
            <DialogHeader>
              <DialogTitle>
                {action.kind === 'create'
                  ? 'Create Workspace user'
                  : action.kind === 'edit'
                    ? 'Edit user profile'
                    : 'Reset user password'}
              </DialogTitle>
            </DialogHeader>
            <UserForm
              connectionId={connectionId}
              tenantId={tenant.id}
              action={action}
              onDone={() => setAction(null)}
            />
          </DialogContent>
        </Dialog>
      )}
      {detail && (
        <Dialog
          open
          onOpenChange={(open) => {
            if (!open) setDetail(null)
          }}
        >
          <DialogContent className='max-h-[85vh] overflow-y-auto sm:max-w-3xl'>
            <DialogHeader>
              <DialogTitle>User details</DialogTitle>
            </DialogHeader>
            <WorkspaceUserDetail
              connectionId={connectionId}
              tenantId={tenant.id}
              userId={detail.id}
            />
          </DialogContent>
        </Dialog>
      )}
      <ConfirmDialog
        open={!!suspend}
        onOpenChange={(open) => {
          if (!open) setSuspend(null)
        }}
        title={`${suspend?.suspended ? 'Restore' : 'Suspend'} ${suspend?.primaryEmail}?`}
        desc={
          <div>
            <p>
              {suspend?.suspended
                ? 'The user will regain access to Google Workspace.'
                : 'The user will lose access to Google Workspace. Their account and data are retained.'}
            </p>
            {error && (
              <p role='alert' className='mt-2 text-destructive'>
                {error}
              </p>
            )}
          </div>
        }
        confirmText={suspend?.suspended ? 'Restore access' : 'Suspend user'}
        destructive={!suspend?.suspended}
        isLoading={changes.suspend.isPending}
        handleConfirm={async () => {
          if (!suspend) return
          try {
            await changes.suspend.mutateAsync({
              userId: suspend.id,
              suspended: !suspend.suspended,
            })
            setSuspend(null)
            toast.success('User access updated')
          } catch (e) {
            setError(String(e))
          } finally {
            changes.suspend.reset()
          }
        }}
      />
    </section>
  )
}
export function WorkspaceUserDetail({
  connectionId,
  tenantId,
  userId,
}: {
  connectionId: string
  tenantId: string
  userId: string
}) {
  const detail = useWorkspaceUser(connectionId, tenantId, userId)
  if (detail.error)
    return (
      <p role='alert' className='text-destructive'>
        {String(detail.error)}
      </p>
    )
  if (!detail.data) return <p>Loading…</p>
  const u = detail.data
  return (
    <Tabs defaultValue='profile'>
      <TabsList>
        <TabsTrigger value='profile'>Profile</TabsTrigger>
        <TabsTrigger value='aliases'>Aliases</TabsTrigger>
        <TabsTrigger value='groups'>Groups</TabsTrigger>
        <TabsTrigger value='security'>Security</TabsTrigger>
      </TabsList>
      <TabsContent value='profile' className='pt-4'>
        {' '}
        <dl className='grid grid-cols-2 gap-3 text-sm'>
          <dt>Email</dt>
          <dd className='break-all'>{u.primaryEmail}</dd>
          <dt>Name</dt>
          <dd>
            {u.givenName} {u.familyName}
          </dd>
          <dt>Customer ID</dt>
          <dd>{u.customerId}</dd>
          <dt>Organization unit</dt>
          <dd>{u.orgUnitPath}</dd>
          <dt>Status</dt>
          <dd>{u.suspended ? 'Suspended' : 'Active'}</dd>
          <dt>Administrator</dt>
          <dd>{triState(u.superAdmin)}</dd>
          <dt>Delegated administrator</dt>
          <dd>{triState(u.delegatedAdmin)}</dd>
          <dt>2-step enrolled / enforced</dt>
          <dd>
            {triState(u.twoStepEnrolled)} / {triState(u.twoStepEnforced)}
          </dd>
          <dt>Last login</dt>
          <dd>{lastLoginText(u.lastLoginTime)}</dd>
          <dt>Password change required</dt>
          <dd>{u.changePasswordAtNextLogin ? 'Yes' : 'No'}</dd>
        </dl>
      </TabsContent>
      <TabsContent value='aliases' className='pt-4'>
        <UserAliases
          connectionId={connectionId}
          tenantId={tenantId}
          userId={userId}
        />
      </TabsContent>
      <TabsContent value='groups' className='pt-4'>
        <UserGroups
          connectionId={connectionId}
          tenantId={tenantId}
          userId={userId}
        />
      </TabsContent>
      <TabsContent value='security' className='pt-4'>
        <UserSecurity
          connectionId={connectionId}
          tenantId={tenantId}
          userId={userId}
        />
      </TabsContent>
    </Tabs>
  )
}
function UserForm({
  connectionId,
  tenantId,
  action,
  onDone,
}: {
  connectionId: string
  tenantId: string
  action: UserAction
  onDone: () => void
}) {
  const user = action.kind === 'create' ? undefined : action.user
  const changes = useWorkspaceUserChanges(connectionId, tenantId)
  const [email, setEmail] = useState(user?.primaryEmail ?? '')
  const [given, setGiven] = useState(user?.givenName ?? '')
  const [family, setFamily] = useState(user?.familyName ?? '')
  const [org, setOrg] = useState(user?.orgUnitPath ?? '/')
  const [password, setPassword] = useState('')
  const [force, setForce] = useState(true)
  const [error, setError] = useState('')
  const pending =
    changes.create.isPending ||
    changes.update.isPending ||
    changes.password.isPending
  async function submit(e: FormEvent) {
    e.preventDefault()
    setError('')
    try {
      const profile = {
        givenName: given.trim(),
        familyName: family.trim(),
        orgUnitPath: org.trim(),
      }
      if (action.kind === 'create')
        await changes.create.mutateAsync({
          primaryEmail: email.trim(),
          profile,
          password,
          changePasswordAtNextLogin: force,
        })
      else if (action.kind === 'edit')
        await changes.update.mutateAsync({ userId: action.user.id, profile })
      else
        await changes.password.mutateAsync({
          userId: action.user.id,
          password,
          changePasswordAtNextLogin: force,
        })
      toast.success(action.kind === 'create' ? 'User created' : 'User updated')
      onDone()
    } catch (e) {
      setError(String(e))
    } finally {
      setPassword('')
      changes.create.reset()
      changes.password.reset()
    }
  }
  return (
    <form onSubmit={submit} className='space-y-4'>
      {user && (
        <p className='text-sm text-muted-foreground'>{user.primaryEmail}</p>
      )}
      {action.kind === 'create' && (
        <div className='space-y-2'>
          <Label htmlFor='ws-user-email'>Primary email</Label>
          <Input
            id='ws-user-email'
            type='email'
            value={email}
            onChange={(e) => setEmail(e.target.value)}
            required
          />
        </div>
      )}
      {action.kind !== 'password' && (
        <>
          <div className='space-y-2'>
            <Label htmlFor='ws-user-given'>Given name</Label>
            <Input
              id='ws-user-given'
              value={given}
              onChange={(e) => setGiven(e.target.value)}
              required
            />
          </div>
          <div className='space-y-2'>
            <Label htmlFor='ws-user-family'>Family name</Label>
            <Input
              id='ws-user-family'
              value={family}
              onChange={(e) => setFamily(e.target.value)}
              required
            />
          </div>
          <OrgUnitPicker
            connectionId={connectionId}
            tenantId={tenantId}
            id='ws-user-org'
            value={org}
            onChange={setOrg}
          />
        </>
      )}
      {action.kind !== 'edit' && (
        <>
          <div className='space-y-2'>
            <Label htmlFor='ws-user-password'>New password</Label>
            <PasswordInput
              id='ws-user-password'
              value={password}
              onChange={(e) => setPassword(e.target.value)}
              minLength={8}
              maxLength={100}
              autoComplete='new-password'
              required
            />
          </div>
          <label className='flex items-center gap-2 text-sm'>
            <input
              type='checkbox'
              checked={force}
              onChange={(e) => setForce(e.target.checked)}
            />
            Require password change at next sign-in
          </label>
        </>
      )}
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
          {pending
            ? 'Saving…'
            : action.kind === 'create'
              ? 'Create user'
              : 'Save'}
        </Button>
      </div>
    </form>
  )
}
