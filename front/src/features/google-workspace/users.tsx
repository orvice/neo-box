import { useState, type FormEvent } from 'react'
import { Link } from '@tanstack/react-router'
import { toast } from 'sonner'
import {
  useWorkspaceUsers,
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
import { ConfirmDialog } from '@/components/confirm-dialog'
import { DataTable, type Column } from '@/components/data-table'
import { PasswordInput } from '@/components/password-input'
import { OrgUnitPicker } from './org-units'

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
        <Button variant='link' className='px-0' asChild>
          <Link
            to='/connections/$connectionId/tenants/$tenantId/users/$userId'
            params={{ connectionId, tenantId: tenant.id, userId: u.id }}
          >
            {u.primaryEmail}
          </Link>
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
