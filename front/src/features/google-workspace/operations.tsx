import { useState } from 'react'
import {
  useWorkspaceOperations,
  WorkspaceOperationStatus,
  type WorkspaceOperation,
} from '@/api/google-workspace'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { DataTable, type Column } from '@/components/data-table'
import { DirectoryError } from './directory-parts'
import { formatWorkspaceTime } from './observability-format'

const states = [
  ['All', WorkspaceOperationStatus.UNSPECIFIED],
  ['Succeeded', WorkspaceOperationStatus.SUCCEEDED],
  ['Failed', WorkspaceOperationStatus.FAILED],
  ['Unknown outcome', WorkspaceOperationStatus.UNKNOWN],
  ['Pending outcome', WorkspaceOperationStatus.PENDING],
] as const
function statusLabel(status: WorkspaceOperationStatus) {
  return states.find((s) => s[1] === status)?.[0] ?? 'Unknown'
}
export function WorkspaceOperations({
  connectionId,
  tenantId = '',
}: {
  connectionId: string
  tenantId?: string
}) {
  const [query, setQuery] = useState('')
  const [search, setSearch] = useState('')
  const [status, setStatus] = useState(WorkspaceOperationStatus.UNSPECIFIED)
  const [page, setPage] = useState(1)
  const [selected, setSelected] = useState<WorkspaceOperation | null>(null)
  const ops = useWorkspaceOperations(
    connectionId,
    tenantId,
    query,
    status,
    page
  )
  const columns: Column<WorkspaceOperation>[] = [
    { header: 'Time', cell: (o) => formatWorkspaceTime(o.createdAt) },
    { header: 'Tenant', accessorKey: 'tenantName' },
    { header: 'Actor', cell: (o) => o.actorName || o.actorId },
    {
      header: 'Action',
      cell: (o) => (
        <Button variant='link' className='px-0' onClick={() => setSelected(o)}>
          {o.action.replace(/_/g, ' ')}
        </Button>
      ),
    },
    {
      header: 'Target',
      cell: (o) => (
        <span className='break-all whitespace-normal'>{o.target}</span>
      ),
    },
    {
      header: 'Result',
      cell: (o) => (
        <Badge
          variant={
            o.status === WorkspaceOperationStatus.FAILED
              ? 'destructive'
              : o.status === WorkspaceOperationStatus.SUCCEEDED
                ? 'secondary'
                : 'outline'
          }
        >
          {statusLabel(o.status)}
        </Badge>
      ),
    },
  ]
  return (
    <section className='space-y-4'>
      <div className='flex items-center justify-between gap-3'>
        <h2 className='text-xl font-semibold'>Neo Box operations</h2>
        <Button
          variant='outline'
          disabled={ops.isFetching}
          onClick={() => void ops.refetch()}
        >
          Refresh
        </Button>
      </div>
      <p className='text-sm text-muted-foreground'>
        Changes initiated through Workspace management in Neo Box. Pending and
        unknown outcomes do not prove whether the change happened; refresh the
        affected resource before retrying. History is retained when a tenant is
        removed.
      </p>
      <div className='flex flex-wrap gap-2'>
        <form
          className='flex flex-1 gap-2'
          onSubmit={(e) => {
            e.preventDefault()
            setQuery(search.trim())
            setPage(1)
          }}
        >
          <Input
            aria-label='Search operations'
            placeholder='Search actor, action, tenant or target'
            value={search}
            onChange={(e) => setSearch(e.target.value)}
          />
          <Button variant='outline'>Search</Button>
        </form>
        <select
          aria-label='Operation result'
          className='h-9 rounded-md border bg-background px-3 text-sm'
          value={status}
          onChange={(e) => {
            setStatus(Number(e.target.value))
            setPage(1)
          }}
        >
          {states.map(([label, value]) => (
            <option key={value} value={value}>
              {label}
            </option>
          ))}
        </select>
      </div>
      <DirectoryError error={ops.error} />
      {!ops.error && (
        <DataTable
          columns={columns}
          data={ops.data?.operations}
          isLoading={ops.isLoading}
          emptyMessage='No matching operations'
        />
      )}
      <div className='flex items-center justify-end gap-3'>
        <span className='text-sm text-muted-foreground'>
          {Number(ops.data?.totalCount ?? 0)} operations · page {page}
        </span>
        <Button
          variant='outline'
          disabled={page <= 1 || ops.isFetching}
          onClick={() => setPage((p) => p - 1)}
        >
          Previous
        </Button>
        <Button
          variant='outline'
          disabled={
            !!ops.error ||
            ops.isFetching ||
            page * 20 >= Number(ops.data?.totalCount ?? 0)
          }
          onClick={() => setPage((p) => p + 1)}
        >
          Next
        </Button>
      </div>
      {selected && (
        <Dialog
          open
          onOpenChange={(open) => {
            if (!open) setSelected(null)
          }}
        >
          <DialogContent className='max-h-[85vh] overflow-y-auto sm:max-w-3xl'>
            <DialogHeader>
              <DialogTitle>{selected.action.replace(/_/g, ' ')}</DialogTitle>
            </DialogHeader>
            <dl className='grid grid-cols-2 gap-2 text-sm'>
              <dt>Tenant</dt>
              <dd>
                {selected.tenantName} · {selected.customerId}
              </dd>
              <dt>Actor</dt>
              <dd>{selected.actorName || selected.actorId}</dd>
              <dt>Result</dt>
              <dd>{statusLabel(selected.status)}</dd>
              <dt>Started</dt>
              <dd>{formatWorkspaceTime(selected.createdAt)}</dd>
              <dt>Finished</dt>
              <dd>{formatWorkspaceTime(selected.finishedAt)}</dd>
            </dl>
            {selected.error && (
              <p className='text-sm text-destructive'>{selected.error}</p>
            )}
            <h3 className='font-semibold'>Requested change</h3>
            <pre className='overflow-auto rounded-md bg-muted p-3 text-xs'>
              {JSON.stringify(
                selected.requested ? selected.requested : {},
                null,
                2
              )}
            </pre>
            <h3 className='font-semibold'>Confirmed result</h3>
            <pre className='overflow-auto rounded-md bg-muted p-3 text-xs'>
              {JSON.stringify(selected.result ? selected.result : {}, null, 2)}
            </pre>
          </DialogContent>
        </Dialog>
      )}
    </section>
  )
}
