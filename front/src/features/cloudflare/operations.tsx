import { useState } from 'react'
import { timestampDate, type Timestamp } from '@bufbuild/protobuf/wkt'
import {
  CloudflareOperationStatus,
  CloudflareRecordAction,
  useCloudflareDNSOperations,
  type CloudflareDNSOperation,
  type CloudflareDNSRecord,
} from '@/api/cloudflare'
import { cn } from '@/lib/utils'
import { Badge } from '@/components/ui/badge'
import { DataTable, type Column } from '@/components/data-table'
import { contentSummary, formatTTL, requestSummary } from './dns'
import { Pager, ProblemAlert } from './list-parts'

const actionLabel: Record<CloudflareRecordAction, string> = {
  [CloudflareRecordAction.UNSPECIFIED]: '-',
  [CloudflareRecordAction.CREATE]: 'Added',
  [CloudflareRecordAction.UPDATE]: 'Changed',
  [CloudflareRecordAction.DELETE]: 'Deleted',
  [CloudflareRecordAction.SET_PROXIED]: 'Proxy',
}

const statusStyle: Record<
  CloudflareOperationStatus,
  { label: string; className: string; hint?: string }
> = {
  [CloudflareOperationStatus.UNSPECIFIED]: {
    label: '-',
    className: 'bg-muted text-muted-foreground',
  },
  [CloudflareOperationStatus.PENDING]: {
    label: 'Pending confirmation',
    className: 'bg-warning-muted text-warning-foreground',
    hint: 'Sent to Cloudflare, but its outcome was never recorded. Check the record.',
  },
  [CloudflareOperationStatus.SUCCEEDED]: {
    label: 'Succeeded',
    className: 'bg-success-muted text-success-foreground',
  },
  [CloudflareOperationStatus.FAILED]: {
    label: 'Failed',
    className: 'bg-danger-muted text-danger-foreground',
  },
  [CloudflareOperationStatus.UNKNOWN]: {
    label: 'Result unknown',
    className: 'bg-warning-muted text-warning-foreground',
    hint: 'Cloudflare never answered; the change may or may not have been made.',
  },
}

/** UTC, as the log is kept. */
function formatUTC(ts: Timestamp | undefined) {
  if (!ts) return '-'
  return timestampDate(ts).toISOString().replace('T', ' ').slice(0, 19) + ' UTC'
}

function RecordState({ r }: { r: CloudflareDNSRecord | undefined }) {
  if (!r) return <span className='text-muted-foreground'>—</span>
  return (
    <div className='max-w-xs space-y-0.5'>
      <div className='truncate font-mono text-xs' title={contentSummary(r)}>
        {r.name} → {contentSummary(r)}
      </div>
      <div className='text-xs text-muted-foreground'>
        TTL {formatTTL(r.ttl)}
        {r.proxiable && (r.proxied ? ' · proxied' : ' · DNS only')}
      </div>
    </div>
  )
}

/** The DNS changes made from Neo Box in one Zone, newest first. */
export function OperationsLog({
  connectionId,
  zoneId,
}: {
  connectionId: string
  zoneId: string
}) {
  const [page, setPage] = useState(1)
  const ops = useCloudflareDNSOperations(connectionId, zoneId, page)

  const columns: Column<CloudflareDNSOperation>[] = [
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
            {actionLabel[o.action]} {o.recordType}
          </div>
          <div className='font-mono text-xs text-muted-foreground'>
            {o.recordName}
          </div>
        </div>
      ),
    },
    { header: 'Before', cell: (o) => <RecordState r={o.before} /> },
    {
      header: 'After',
      cell: (o) =>
        o.after || !o.requested ? (
          <RecordState r={o.after} />
        ) : (
          // Nothing was confirmed; show what was asked for, as such.
          <div className='max-w-xs space-y-0.5 text-xs'>
            <div className='text-muted-foreground'>Requested</div>
            <div className='font-mono break-words whitespace-normal'>
              {requestSummary(o.requested)}
            </div>
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
        dashboard or other tools are not.
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
