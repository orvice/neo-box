import { useState, type FormEvent } from 'react'
import { toast } from 'sonner'
import {
  useWorkspaceOrgUnits,
  useWorkspaceDirectoryChanges,
  type WorkspaceOrgUnit,
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
  DirectoryConfirm,
  DirectoryError,
  Field,
  FormButtons,
} from './directory-parts'
import { ORG_SCOPE } from './directory-state'
import { orgUnitOptions, orgUnitTreeRows } from './org-unit-tree'

type Props = { connectionId: string; tenantId: string }
export function WorkspaceOrgUnits(props: Props) {
  const units = useWorkspaceOrgUnits(props.connectionId, props.tenantId)
  const changes = useWorkspaceDirectoryChanges(
    props.connectionId,
    props.tenantId
  )
  const [form, setForm] = useState<WorkspaceOrgUnit | 'new' | null>(null)
  const [remove, setRemove] = useState<WorkspaceOrgUnit | null>(null)
  const rows = orgUnitTreeRows(units.data ?? [])
  const columns: Column<WorkspaceOrgUnit & { depth: number }>[] = [
    {
      header: 'Organization',
      cell: (o) => (
        <div style={{ paddingInlineStart: `${o.depth * 16}px` }}>
          <p className='font-medium'>{o.name}</p>
          <p className='text-xs text-muted-foreground'>{o.path}</p>
        </div>
      ),
    },
    { header: 'Description', accessorKey: 'description' },
    {
      header: 'Actions',
      cell: (o) =>
        o.id ? (
          <div className='flex gap-2'>
            <Button size='sm' variant='outline' onClick={() => setForm(o)}>
              Edit / move
            </Button>
            <Button size='sm' variant='outline' onClick={() => setRemove(o)}>
              Delete
            </Button>
          </div>
        ) : null,
    },
  ]
  return (
    <section className='space-y-4'>
      <div className='flex items-center justify-between gap-3'>
        <h2 className='text-xl font-semibold'>Organization units</h2>
        <div className='flex gap-2'>
          <Button
            variant='outline'
            disabled={units.isFetching}
            onClick={() => void units.refetch()}
          >
            Refresh
          </Button>
          <Button onClick={() => setForm('new')}>
            Create organization unit
          </Button>
        </div>
      </div>
      <p className='text-sm text-muted-foreground'>
        Edit a user's profile to move that user to another organization unit.
        Moving or renaming a unit affects its subtree and may change inherited
        Google policies.
      </p>
      <DirectoryError error={units.error} scope={ORG_SCOPE} />
      {!units.error && (
        <DataTable columns={columns} data={rows} isLoading={units.isLoading} />
      )}{' '}
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
                  ? 'Create organization unit'
                  : 'Edit / move organization unit'}
              </DialogTitle>
            </DialogHeader>
            <OrgUnitForm
              {...props}
              units={units.data ?? []}
              unit={form === 'new' ? undefined : form}
              onDone={() => setForm(null)}
            />
          </DialogContent>
        </Dialog>
      )}
      {remove && (
        <DirectoryConfirm
          title={`Delete ${remove.path}?`}
          description='Delete this organization unit in Google. Google will reject the request if its deletion requirements are not met. No users are deleted by Neo Box.'
          confirmText='Delete organization unit'
          onClose={() => setRemove(null)}
          run={() => changes.deleteOrgUnit.mutateAsync(remove.id)}
        />
      )}
    </section>
  )
}
function OrgUnitForm({
  connectionId,
  tenantId,
  units,
  unit,
  onDone,
}: Props & {
  units: WorkspaceOrgUnit[]
  unit?: WorkspaceOrgUnit
  onDone: () => void
}) {
  const changes = useWorkspaceDirectoryChanges(connectionId, tenantId)
  const [name, setName] = useState(unit?.name ?? '')
  const [description, setDescription] = useState(unit?.description ?? '')
  const [parent, setParent] = useState(unit?.parentPath || '/')
  const [error, setError] = useState('')
  const options = orgUnitOptions(units, unit?.path)
  const pending =
    changes.createOrgUnit.isPending || changes.updateOrgUnit.isPending
  async function submit(e: FormEvent) {
    e.preventDefault()
    setError('')
    try {
      const input = { name: name.trim(), description, parentPath: parent }
      if (unit)
        await changes.updateOrgUnit.mutateAsync({
          orgUnitId: unit.id,
          orgUnit: input,
        })
      else await changes.createOrgUnit.mutateAsync(input)
      toast.success('Organization unit saved')
      onDone()
    } catch (e) {
      setError(String(e))
    }
  }
  return (
    <form onSubmit={submit} className='space-y-4'>
      <Field
        id='ws-ou-name'
        label='Name'
        value={name}
        onChange={setName}
        required
      />
      <Field
        id='ws-ou-description'
        label='Description'
        value={description}
        onChange={setDescription}
      />
      <div className='space-y-2'>
        <Label htmlFor='ws-ou-parent'>Parent organization</Label>
        <select
          id='ws-ou-parent'
          value={parent}
          onChange={(e) => setParent(e.target.value)}
          className='h-9 w-full rounded-md border bg-background px-3 text-sm'
        >
          {options.map((path) => (
            <option key={path} value={path}>
              {path}
            </option>
          ))}
        </select>
      </div>
      <DirectoryError error={error} scope={ORG_SCOPE} />
      <FormButtons pending={pending} onCancel={onDone} />
    </form>
  )
}
// Keep the existing manually entered path available if the optional OU scope
// has not been authorized, or if visibility is restricted by admin privileges.
export function OrgUnitPicker({
  connectionId,
  tenantId,
  id,
  value,
  onChange,
}: {
  connectionId: string
  tenantId: string
  id: string
  value: string
  onChange: (value: string) => void
}) {
  const units = useWorkspaceOrgUnits(connectionId, tenantId)
  const options = orgUnitOptions(units.data ?? [])
  return (
    <div className='space-y-2'>
      <Label htmlFor={id}>Organization unit path</Label>
      {units.data && !units.error ? (
        <select
          id={id}
          value={value}
          onChange={(e) => onChange(e.target.value)}
          className='h-9 w-full rounded-md border bg-background px-3 text-sm'
        >
          {!options.includes(value) && (
            <option value={value}>{value} (current)</option>
          )}
          {options.map((path) => (
            <option key={path} value={path}>
              {path}
            </option>
          ))}
        </select>
      ) : (
        <Input
          id={id}
          value={value}
          onChange={(e) => onChange(e.target.value)}
          placeholder='/'
          required
        />
      )}
      {units.error && (
        <p className='text-xs text-muted-foreground'>
          Organization choices could not be loaded. Enter the path manually, or
          authorize <code className='break-all'>{ORG_SCOPE}</code> and check the
          administrator's privileges.
        </p>
      )}
    </div>
  )
}
