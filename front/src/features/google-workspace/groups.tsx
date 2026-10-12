import { useState, type FormEvent } from 'react'
import { toast } from 'sonner'
import {
  useWorkspaceGroups,
  useWorkspaceMembers,
  useWorkspaceDirectoryChanges,
  type WorkspaceGroup,
  type WorkspaceGroupMember,
} from '@/api/google-workspace'
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { DataTable, type Column } from '@/components/data-table'
import {
  CursorPager,
  DirectoryError,
  DirectoryConfirm,
  Field,
  FormButtons,
} from './directory-parts'
import { GROUP_SCOPE, useCursor } from './directory-state'

type Props = { connectionId: string; tenantId: string }
export function WorkspaceGroups(props: Props) {
  const { connectionId, tenantId } = props
  const cursor = useCursor()
  const [query, setQuery] = useState('')
  const [search, setSearch] = useState('')
  const groups = useWorkspaceGroups(connectionId, tenantId, query, cursor.token)
  const changes = useWorkspaceDirectoryChanges(connectionId, tenantId)
  const [form, setForm] = useState<WorkspaceGroup | 'new' | null>(null)
  const [remove, setRemove] = useState<WorkspaceGroup | null>(null)
  const [selected, setSelected] = useState<WorkspaceGroup | null>(null)
  const columns: Column<WorkspaceGroup>[] = [
    {
      header: 'Group',
      cell: (g) => (
        <Button variant='link' className='px-0' onClick={() => setSelected(g)}>
          {g.email}
        </Button>
      ),
    },
    { header: 'Name', accessorKey: 'name' },
    { header: 'Direct members', accessorKey: 'directMemberCount' },
    {
      header: 'Actions',
      cell: (g) => (
        <div className='flex gap-2'>
          <Button size='sm' variant='outline' onClick={() => setForm(g)}>
            Edit
          </Button>
          <Button size='sm' variant='outline' onClick={() => setRemove(g)}>
            Delete
          </Button>
        </div>
      ),
    },
  ]
  return (
    <section className='space-y-4'>
      <div className='flex items-center justify-between gap-3'>
        <h2 className='text-xl font-semibold'>Groups</h2>
        <div className='flex gap-2'>
          <Button
            variant='outline'
            disabled={groups.isFetching}
            onClick={() => void groups.refetch()}
          >
            Refresh
          </Button>
          <Button onClick={() => setForm('new')}>Create group</Button>
        </div>
      </div>
      <form
        onSubmit={(e) => {
          e.preventDefault()
          setQuery(search.trim())
          cursor.reset()
        }}
        className='flex gap-2'
      >
        <Input
          aria-label='Google Directory group query'
          placeholder='Group query, e.g. email:team*'
          value={search}
          onChange={(e) => setSearch(e.target.value)}
        />
        <Button variant='outline'>Search</Button>
      </form>
      <DirectoryError error={groups.error} scope={GROUP_SCOPE} />
      {!groups.error && (
        <DataTable
          columns={columns}
          data={groups.data?.groups}
          isLoading={groups.isLoading}
          emptyMessage={query ? 'No groups match' : 'No groups in this tenant'}
        />
      )}
      <CursorPager
        cursor={cursor}
        nextToken={groups.error ? undefined : groups.data?.nextPageToken}
        busy={groups.isFetching}
      />
      {selected && (
        <GroupMembers key={selected.id} {...props} group={selected} />
      )}
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
                {form === 'new' ? 'Create group' : 'Edit group'}
              </DialogTitle>
            </DialogHeader>
            <GroupForm
              {...props}
              group={form === 'new' ? undefined : form}
              onDone={() => setForm(null)}
            />
          </DialogContent>
        </Dialog>
      )}
      {remove && (
        <DirectoryConfirm
          title={`Delete ${remove.email}?`}
          description='Delete this Google group and its membership configuration. This removes the group in Google, not just its display in Neo Box.'
          confirmText='Delete group'
          onClose={() => setRemove(null)}
          run={async () => {
            await changes.deleteGroup.mutateAsync(remove.id)
            if (selected?.id === remove.id) setSelected(null)
          }}
        />
      )}
    </section>
  )
}
function GroupForm({
  connectionId,
  tenantId,
  group,
  onDone,
}: Props & { group?: WorkspaceGroup; onDone: () => void }) {
  const changes = useWorkspaceDirectoryChanges(connectionId, tenantId)
  const [email, setEmail] = useState(group?.email ?? '')
  const [name, setName] = useState(group?.name ?? '')
  const [description, setDescription] = useState(group?.description ?? '')
  const [error, setError] = useState('')
  const pending = changes.createGroup.isPending || changes.updateGroup.isPending
  async function submit(e: FormEvent) {
    e.preventDefault()
    setError('')
    try {
      const input = { email: email.trim(), name: name.trim(), description }
      if (group)
        await changes.updateGroup.mutateAsync({
          groupId: group.id,
          group: input,
        })
      else await changes.createGroup.mutateAsync(input)
      toast.success('Group saved')
      onDone()
    } catch (e) {
      setError(String(e))
    }
  }
  return (
    <form onSubmit={submit} className='space-y-4'>
      <Field
        id='ws-group-email'
        label='Group email'
        type='email'
        value={email}
        onChange={setEmail}
        required
      />
      <Field
        id='ws-group-name'
        label='Name'
        value={name}
        onChange={setName}
        required
      />
      <Field
        id='ws-group-description'
        label='Description'
        value={description}
        onChange={setDescription}
      />
      <DirectoryError error={error} scope={GROUP_SCOPE} />
      <FormButtons pending={pending} onCancel={onDone} />
    </form>
  )
}
function GroupMembers({
  connectionId,
  tenantId,
  group,
}: Props & { group: WorkspaceGroup }) {
  const cursor = useCursor()
  const members = useWorkspaceMembers(
    connectionId,
    tenantId,
    group.id,
    cursor.token
  )
  const changes = useWorkspaceDirectoryChanges(connectionId, tenantId)
  const [form, setForm] = useState<WorkspaceGroupMember | 'new' | null>(null)
  const [remove, setRemove] = useState<WorkspaceGroupMember | null>(null)
  const columns: Column<WorkspaceGroupMember>[] = [
    { header: 'Member', cell: (m) => m.email || m.id },
    { header: 'Role', accessorKey: 'role' },
    { header: 'Type', accessorKey: 'type' },
    { header: 'Status', accessorKey: 'status' },
    {
      header: 'Actions',
      cell: (m) => (
        <div className='flex gap-2'>
          <Button size='sm' variant='outline' onClick={() => setForm(m)}>
            Change role
          </Button>
          <Button size='sm' variant='outline' onClick={() => setRemove(m)}>
            Remove
          </Button>
        </div>
      ),
    },
  ]
  return (
    <div className='space-y-4 border-t pt-6'>
      <div className='flex items-center justify-between gap-3'>
        <div>
          <h3 className='text-lg font-semibold'>{group.email}</h3>
          <p className='text-sm text-muted-foreground'>
            Direct members. External addresses and nested groups are allowed
            when Google permits them.
          </p>
        </div>
        <Button onClick={() => setForm('new')}>Add member</Button>
      </div>
      <DirectoryError error={members.error} scope={GROUP_SCOPE} />
      {!members.error && (
        <DataTable
          columns={columns}
          data={members.data?.members}
          isLoading={members.isLoading}
          emptyMessage='No direct members'
        />
      )}
      <CursorPager
        cursor={cursor}
        nextToken={members.error ? undefined : members.data?.nextPageToken}
        busy={members.isFetching}
      />
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
                {form === 'new' ? 'Add group member' : 'Change member role'}
              </DialogTitle>
            </DialogHeader>
            <MemberForm
              connectionId={connectionId}
              tenantId={tenantId}
              groupId={group.id}
              member={form === 'new' ? undefined : form}
              onDone={() => setForm(null)}
            />
          </DialogContent>
        </Dialog>
      )}
      {remove && (
        <DirectoryConfirm
          title={`Remove ${remove.email || remove.id}?`}
          description={`Remove this membership from ${group.email}. The member's account and other memberships are retained.`}
          confirmText='Remove member'
          onClose={() => setRemove(null)}
          run={() =>
            changes.deleteMember.mutateAsync({
              groupId: group.id,
              memberId: remove.id,
            })
          }
        />
      )}
    </div>
  )
}
function MemberForm({
  connectionId,
  tenantId,
  groupId,
  member,
  onDone,
}: Props & {
  groupId: string
  member?: WorkspaceGroupMember
  onDone: () => void
}) {
  const changes = useWorkspaceDirectoryChanges(connectionId, tenantId)
  const [email, setEmail] = useState(member?.email ?? '')
  const [role, setRole] = useState(member?.role ?? 'MEMBER')
  const [error, setError] = useState('')
  const pending = changes.addMember.isPending || changes.updateMember.isPending
  async function submit(e: FormEvent) {
    e.preventDefault()
    setError('')
    try {
      if (member)
        await changes.updateMember.mutateAsync({
          groupId,
          memberId: member.id,
          role,
        })
      else
        await changes.addMember.mutateAsync({
          groupId,
          email: email.trim(),
          role,
        })
      toast.success('Membership saved')
      onDone()
    } catch (e) {
      setError(String(e))
    }
  }
  return (
    <form onSubmit={submit} className='space-y-4'>
      {member ? (
        <p className='text-sm'>{member.email || member.id}</p>
      ) : (
        <Field
          id='ws-member-email'
          label='Member email'
          type='email'
          value={email}
          onChange={setEmail}
          required
        />
      )}
      <div className='space-y-2'>
        <Label htmlFor='ws-member-role'>Role</Label>
        <select
          id='ws-member-role'
          className='h-9 w-full rounded-md border bg-background px-3 text-sm'
          value={role}
          onChange={(e) => setRole(e.target.value)}
        >
          {['MEMBER', 'MANAGER', 'OWNER'].map((r) => (
            <option key={r} value={r}>
              {r}
            </option>
          ))}
        </select>
      </div>
      <p className='text-xs text-muted-foreground'>
        Owner and Manager roles grant group management privileges. Google
        enforces restrictions on removing or demoting owners.
      </p>
      <DirectoryError error={error} scope={GROUP_SCOPE} />
      <FormButtons pending={pending} onCancel={onDone} />
    </form>
  )
}
