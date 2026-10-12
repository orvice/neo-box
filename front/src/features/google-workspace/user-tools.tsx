import { useState, type FormEvent } from 'react'
import { toast } from 'sonner'
import {
  useWorkspaceAliases,
  useWorkspaceUserGroups,
  useWorkspaceTokens,
  useWorkspaceDirectoryChanges,
  type WorkspaceAlias,
  type WorkspaceGroup,
  type WorkspaceUserToken,
} from '@/api/google-workspace'
import { Button } from '@/components/ui/button'
import { DataTable, type Column } from '@/components/data-table'
import { DirectoryError, DirectoryConfirm, Field } from './directory-parts'
import { GROUP_SCOPE, SECURITY_SCOPE } from './directory-state'

type Props = { connectionId: string; tenantId: string; userId: string }
export function UserAliases(props: Props) {
  const { connectionId, tenantId, userId } = props
  const aliases = useWorkspaceAliases(connectionId, tenantId, userId)
  const changes = useWorkspaceDirectoryChanges(connectionId, tenantId)
  const [email, setEmail] = useState('')
  const [error, setError] = useState('')
  const [remove, setRemove] = useState<WorkspaceAlias | null>(null)
  async function add(e: FormEvent) {
    e.preventDefault()
    setError('')
    try {
      await changes.addAlias.mutateAsync({ userId, alias: email.trim() })
      setEmail('')
      toast.success('Alias added')
    } catch (e) {
      setError(String(e))
    }
  }
  const columns: Column<WorkspaceAlias>[] = [
    { header: 'Alias', accessorKey: 'email' },
    {
      header: 'Management',
      cell: (a) => (a.editable ? 'Editable' : 'Managed by Google'),
    },
    {
      header: 'Actions',
      cell: (a) =>
        a.editable ? (
          <Button size='sm' variant='outline' onClick={() => setRemove(a)}>
            Remove
          </Button>
        ) : null,
    },
  ]
  return (
    <div className='space-y-4'>
      <form onSubmit={add} className='flex items-end gap-2'>
        <div className='flex-1'>
          <Field
            id='ws-alias-email'
            label='New email alias'
            value={email}
            onChange={setEmail}
            type='email'
            required
          />
        </div>
        <Button
          disabled={
            changes.addAlias.isPending || aliases.isLoading || !!aliases.error
          }
        >
          Add alias
        </Button>
      </form>
      <DirectoryError error={error || aliases.error} />
      {!aliases.error && (
        <DataTable
          columns={columns}
          data={aliases.data}
          isLoading={aliases.isLoading}
          emptyMessage='No aliases'
        />
      )}
      {remove && (
        <DirectoryConfirm
          title={`Remove ${remove.email}?`}
          description='Mail addressed to this alias will no longer be delivered to this user.'
          confirmText='Remove alias'
          onClose={() => setRemove(null)}
          run={() =>
            changes.deleteAlias.mutateAsync({ userId, alias: remove.email })
          }
        />
      )}
    </div>
  )
}
export function UserGroups(props: Props) {
  const groups = useWorkspaceUserGroups(
    props.connectionId,
    props.tenantId,
    props.userId
  )
  const columns: Column<WorkspaceGroup>[] = [
    { header: 'Group', accessorKey: 'email' },
    { header: 'Name', accessorKey: 'name' },
    { header: 'Description', accessorKey: 'description' },
  ]
  return (
    <div className='space-y-4'>
      <p className='text-sm text-muted-foreground'>
        Memberships in this tenant. Manage members in the tenant's Groups tab.
      </p>
      <DirectoryError error={groups.error} scope={GROUP_SCOPE} />
      {!groups.error && (
        <DataTable
          columns={columns}
          data={groups.data}
          isLoading={groups.isLoading}
          emptyMessage='No groups in this tenant'
        />
      )}
    </div>
  )
}
export function UserSecurity(props: Props) {
  const { connectionId, tenantId, userId } = props
  const tokens = useWorkspaceTokens(connectionId, tenantId, userId)
  const changes = useWorkspaceDirectoryChanges(connectionId, tenantId)
  const [signOut, setSignOut] = useState(false)
  const [revoke, setRevoke] = useState<WorkspaceUserToken | null>(null)
  const columns: Column<WorkspaceUserToken>[] = [
    {
      header: 'Application',
      cell: (t) => (
        <div>
          <p className='font-medium'>{t.displayText || t.clientId}</p>
          <p className='text-xs break-all text-muted-foreground'>
            {t.clientId}
          </p>
        </div>
      ),
    },
    {
      header: 'Granted scopes',
      cell: (t) => (
        <ul className='space-y-1 text-xs break-all whitespace-normal'>
          {t.scopes.map((s) => (
            <li key={s}>{s}</li>
          ))}
        </ul>
      ),
    },
    { header: 'Type', cell: (t) => (t.nativeApp ? 'Native app' : 'Web app') },
    {
      header: 'Actions',
      cell: (t) => (
        <Button size='sm' variant='outline' onClick={() => setRevoke(t)}>
          Revoke access
        </Button>
      ),
    },
  ]
  return (
    <div className='space-y-4'>
      <div className='flex flex-wrap items-center justify-between gap-3'>
        <p className='text-sm text-muted-foreground'>
          Sign out all web and device sessions. Application grants are managed
          separately.
        </p>
        <Button variant='outline' onClick={() => setSignOut(true)}>
          Force sign-out
        </Button>
      </div>
      <h3 className='font-semibold'>Third-party application grants</h3>
      <DirectoryError error={tokens.error} scope={SECURITY_SCOPE} />
      {!tokens.error && (
        <DataTable
          columns={columns}
          data={tokens.data}
          isLoading={tokens.isLoading}
          emptyMessage='No third-party application grants'
        />
      )}
      {signOut && (
        <DirectoryConfirm
          title='Force this user to sign out?'
          description='Google will reset sign-in cookies and sign out web and device sessions. The user must authenticate again. This does not revoke application grants.'
          confirmText='Force sign-out'
          onClose={() => setSignOut(false)}
          run={() => changes.signOut.mutateAsync(userId)}
        />
      )}{' '}
      {revoke && (
        <DirectoryConfirm
          title={`Revoke ${revoke.displayText || revoke.clientId}?`}
          description='Remove this application’s existing access grants for the user. This does not permanently block the application from being authorized again.'
          confirmText='Revoke access'
          onClose={() => setRevoke(null)}
          run={() =>
            changes.revokeToken.mutateAsync({
              userId,
              clientId: revoke.clientId,
            })
          }
        />
      )}
    </div>
  )
}
