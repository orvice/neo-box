import { useState } from 'react'
import { Link } from '@tanstack/react-router'
import { ExternalLink, TriangleAlert } from 'lucide-react'
import { Provider, useConnections } from '@/api/connections'
import {
  RestoreStatus,
  isRestoreActive,
  type Restore,
  type RestoreWarning,
} from '@/api/nocodb'
import { formatCount, formatTime } from '@/lib/format'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import { DataTable, type Column } from '@/components/data-table'
import { formatDuration, nocodbBaseLink, nocodbBaseUrl } from './format'
import { RestoreStatusBadge } from './status-badge'

const warningLabels: Record<string, string> = {
  field_skipped: 'Field not restored',
  attachments_skipped: 'Attachments not restored',
  user_values_dropped: 'User values dropped',
  records_failed: 'Records rejected',
  links_failed: 'Links not restored',
  display_field_not_set: 'Display field not set',
  relation_guessed: 'Relation paired by order',
}

/**
 * Restores with their status, where they went, and what they couldn't bring
 * back. showSource adds the snapshot each restore came from.
 */
export function RestoreTable({
  restores,
  isLoading,
  highlightId,
  showSource,
}: {
  restores: Restore[] | undefined
  isLoading: boolean
  highlightId?: string
  showSource?: boolean
}) {
  const connections = useConnections(Provider.NOCODB)
  const [warningsOf, setWarningsOf] = useState<Restore | null>(null)
  const connection = (id: string) => connections.data?.find((c) => c.id === id)

  const columns: Column<Restore>[] = [
    {
      header: 'Started',
      cell: (r) => (
        <div>
          <div className='flex items-center gap-2 text-sm'>
            {formatTime(r.createdAt)}
            {r.id === highlightId && <Badge variant='secondary'>New</Badge>}
          </div>
          {!isRestoreActive(r) && (
            <div className='text-xs text-muted-foreground'>
              took {formatDuration(r)}
            </div>
          )}
        </div>
      ),
    },
    {
      header: 'Status',
      cell: (r) => (
        <div className='max-w-xs space-y-1 whitespace-normal'>
          <RestoreStatusBadge status={r.status} />
          {isRestoreActive(r) && r.progress && (
            <div className='text-xs text-muted-foreground'>{r.progress}</div>
          )}
          {r.status === RestoreStatus.FAILED && r.error && (
            <div
              className='line-clamp-2 text-xs text-danger-foreground'
              title={r.error}
            >
              {r.error}
            </div>
          )}
        </div>
      ),
    },
    ...(showSource
      ? [
          {
            header: 'From',
            cell: (r: Restore) => (
              <div className='max-w-48 whitespace-normal'>
                <Link
                  to='/connections/$connectionId/snapshots/$snapshotId'
                  params={{
                    connectionId: r.sourceConnectionId,
                    snapshotId: r.snapshotId,
                  }}
                  className='text-sm font-medium hover:underline'
                >
                  {r.sourceBaseTitle || r.sourceBaseId}
                </Link>
                <div className='text-xs text-muted-foreground'>
                  {connection(r.sourceConnectionId)?.name ??
                    'deleted connection'}
                </div>
              </div>
            ),
          },
        ]
      : []),
    {
      header: 'Into',
      cell: (r) => {
        const target = connection(r.targetConnectionId)
        const href = nocodbBaseLink(nocodbBaseUrl(target), r.targetBaseId)
        const partial = r.status === RestoreStatus.FAILED && r.targetBaseId
        return (
          <div className='max-w-xs whitespace-normal'>
            <div className='text-sm font-medium break-words'>
              {r.targetBaseTitle}
            </div>
            <div className='text-xs text-muted-foreground'>
              {target?.name ?? 'deleted connection'}
            </div>
            {href && (
              <a
                href={href}
                target='_blank'
                rel='noreferrer'
                className='inline-flex items-center gap-1 text-xs hover:underline'
              >
                <ExternalLink className='h-3 w-3' />
                {partial ? `Partial base ${r.targetBaseId}` : 'Open in NocoDB'}
              </a>
            )}
          </div>
        )
      },
    },
    {
      header: 'Restored',
      // A restore cut short by a restart counted nothing; don't show zeros.
      cell: (r) =>
        r.status === RestoreStatus.SUCCEEDED ||
        (r.status === RestoreStatus.FAILED && r.tableCount > 0) ? (
          <div className='space-y-1'>
            <div className='text-xs text-muted-foreground'>
              {r.tableCount} tables · {formatCount(r.recordCount)} records ·{' '}
              {formatCount(r.linkCount)} links
            </div>
            {r.warnings.length > 0 && (
              <Button
                size='sm'
                variant='ghost'
                className='h-7 px-2 text-xs'
                onClick={() => setWarningsOf(r)}
              >
                <TriangleAlert />
                {r.warnings.length}{' '}
                {r.warnings.length === 1 ? 'warning' : 'warnings'}
              </Button>
            )}
          </div>
        ) : (
          <span className='text-xs text-muted-foreground'>-</span>
        ),
    },
  ]

  return (
    <>
      <DataTable
        columns={columns}
        data={restores}
        isLoading={isLoading}
        emptyMessage='No restores yet.'
      />
      {warningsOf && (
        <WarningsDialog
          restore={warningsOf}
          onClose={() => setWarningsOf(null)}
        />
      )}
    </>
  )
}

function WarningsDialog({
  restore,
  onClose,
}: {
  restore: Restore
  onClose: () => void
}) {
  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent className='sm:max-w-3xl'>
        <DialogHeader>
          <DialogTitle>What wasn't restored</DialogTitle>
          <DialogDescription>
            {restore.targetBaseTitle} · grouped by table and field
          </DialogDescription>
        </DialogHeader>
        <div className='max-h-[60vh] overflow-auto rounded-md border'>
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Warning</TableHead>
                <TableHead>Where</TableHead>
                <TableHead className='text-end'>Count</TableHead>
                <TableHead>Detail</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {restore.warnings.map((w, i) => (
                <TableRow key={i}>
                  <TableCell className='text-sm whitespace-nowrap'>
                    {warningLabels[w.code] ?? w.code}
                  </TableCell>
                  <TableCell className='text-sm'>{where(w)}</TableCell>
                  <TableCell className='text-end text-sm'>
                    {formatCount(w.count)}
                  </TableCell>
                  <TableCell className='text-xs text-muted-foreground'>
                    {w.message}
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </div>
      </DialogContent>
    </Dialog>
  )
}

function where(w: RestoreWarning) {
  if (w.table && w.field) return `${w.table} / ${w.field}`
  return w.table || w.field || '-'
}
