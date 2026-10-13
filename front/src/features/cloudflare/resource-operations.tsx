import { useState } from 'react'
import {
  CloudflareOperationStatus,
  useCloudflareResourceOperations,
  type CloudflareResourceKind,
  type CloudflareResourceOperation,
} from '@/api/cloudflare'
import { cn } from '@/lib/utils'
import { Badge } from '@/components/ui/badge'
import { DataTable, type Column } from '@/components/data-table'
import { Pager, ProblemAlert } from './list-parts'
import { actionDetail, actionLabel, stateSummary } from './manage'
import { formatUTC, statusStyle } from './operation-status'

function State({ label, text }: { label: string; text: string }) {
  if (!text) return null
  return (
    <div className='max-w-sm text-xs'>
      <span className='text-muted-foreground'>{label} </span>
      <span className='font-mono break-words whitespace-normal'>{text}</span>
    </div>
  )
}

/** The changes made from Neo Box to one Worker or Pages project, newest first. */
export function ResourceOperationsLog({
  connectionId,
  kind,
  resource,
}: {
  connectionId: string
  kind: CloudflareResourceKind
  resource: string
}) {
  const [page, setPage] = useState(1)
  const ops = useCloudflareResourceOperations(
    connectionId,
    kind,
    resource,
    page
  )

  const columns: Column<CloudflareResourceOperation>[] = [
    {
      header: 'When',
      cell: (o) => (
        <span className='font-mono text-xs whitespace-nowrap'>
          {formatUTC(o.createdAt)}
        </span>
      ),
    },
    {
      header: 'Change',
      cell: (o) => (
        <div>
          <div className='font-medium'>
            {actionLabel(o)} {actionDetail(o)}
          </div>
          {o.target && (
            <div className='font-mono text-xs break-all text-muted-foreground'>
              {o.target}
            </div>
          )}
        </div>
      ),
    },
    {
      header: 'Details',
      cell: (o) => (
        <div className='space-y-0.5'>
          <State label='Before' text={stateSummary(o.before)} />
          {o.after ? (
            <State label='After' text={stateSummary(o.after)} />
          ) : (
            // Nothing was confirmed; show what was asked for, as such.
            <State label='Requested' text={stateSummary(o.requested)} />
          )}
        </div>
      ),
    },
    {
      header: 'Result',
      cell: (o) => {
        const s =
          statusStyle[o.status] ??
          statusStyle[CloudflareOperationStatus.UNSPECIFIED]
        return (
          <div className='max-w-xs space-y-1'>
            <Badge className={cn(s.className)} title={s.hint}>
              {s.label}
            </Badge>
            {(o.error || s.hint) && (
              <div className='text-xs break-words whitespace-normal text-muted-foreground'>
                {o.error || s.hint}
              </div>
            )}
          </div>
        )
      },
    },
    {
      header: 'By',
      cell: (o) => <span className='text-xs'>{o.actorName || o.actorId}</span>,
    },
  ]

  return (
    <div className='space-y-4'>
      <p className='text-sm text-muted-foreground'>
        Only changes made from Neo Box are listed; changes in the Cloudflare
        dashboard or other tools are not. Secret values are never kept.
      </p>
      {ops.error ? (
        <ProblemAlert error={ops.error} />
      ) : (
        <DataTable
          columns={columns}
          data={ops.data?.operations}
          isLoading={ops.isLoading}
          emptyMessage='No changes made from Neo Box yet'
        />
      )}
      <Pager info={ops.data?.pageInfo} onPage={setPage} busy={ops.isFetching} />
    </div>
  )
}
