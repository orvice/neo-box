import { useState } from 'react'
import { Link } from '@tanstack/react-router'
import { Download, Eye, Trash2 } from 'lucide-react'
import { toast } from 'sonner'
import {
  SnapshotStatus,
  downloadSnapshot,
  isSnapshotActive,
  useDeleteSnapshot,
  type Snapshot,
} from '@/api/nocodb'
import { Button } from '@/components/ui/button'
import { ConfirmDialog } from '@/components/confirm-dialog'
import { DataTable, type Column } from '@/components/data-table'
import { formatBytes, formatCount, formatTime, triggerLabel } from './format'
import { SnapshotStatusBadge } from './status-badge'

export function SnapshotTable({
  snapshots,
  isLoading,
  hideBase,
}: {
  snapshots: Snapshot[] | undefined
  isLoading: boolean
  hideBase?: boolean
}) {
  const remove = useDeleteSnapshot()
  const [deleting, setDeleting] = useState<Snapshot | null>(null)

  const columns: Column<Snapshot>[] = [
    ...(hideBase
      ? []
      : [
          {
            header: 'Base',
            cell: (s: Snapshot) => (
              <span className='font-medium'>{s.baseTitle || s.baseId}</span>
            ),
          },
        ]),
    {
      header: 'Taken',
      cell: (s) => (
        <div>
          <div className='text-sm'>{formatTime(s.createdAt)}</div>
          <div className='text-xs text-muted-foreground'>
            {triggerLabel(s.trigger)}
          </div>
        </div>
      ),
    },
    {
      header: 'Status',
      cell: (s) => (
        <div className='max-w-xs space-y-1'>
          <SnapshotStatusBadge status={s.status} />
          {isSnapshotActive(s) && s.progress && (
            <div className='text-xs text-muted-foreground'>{s.progress}</div>
          )}
          {s.status === SnapshotStatus.FAILED && s.error && (
            <div className='line-clamp-2 text-xs text-danger-foreground'>
              {s.error}
            </div>
          )}
        </div>
      ),
    },
    {
      header: 'Content',
      cell: (s) =>
        s.status === SnapshotStatus.SUCCEEDED ? (
          <div className='text-xs text-muted-foreground'>
            {s.tables.length} tables · {formatCount(s.recordCount)} records ·{' '}
            {formatCount(s.linkCount)} links · {formatBytes(s.sizeBytes)}
          </div>
        ) : (
          <span className='text-xs text-muted-foreground'>-</span>
        ),
    },
    {
      header: 'Actions',
      cell: (s) => (
        <div className='flex flex-wrap gap-1'>
          <Button asChild size='sm' variant='ghost'>
            <Link
              to='/nocodb/snapshots/$snapshotId'
              params={{ snapshotId: s.id }}
            >
              <Eye />
              View
            </Link>
          </Button>
          <Button
            size='sm'
            variant='ghost'
            disabled={s.status !== SnapshotStatus.SUCCEEDED}
            onClick={() =>
              downloadSnapshot(s.id).catch((e: Error) => toast.error(e.message))
            }
          >
            <Download />
          </Button>
          <Button
            size='sm'
            variant='ghost'
            disabled={isSnapshotActive(s)}
            onClick={() => setDeleting(s)}
          >
            <Trash2 />
          </Button>
        </div>
      ),
    },
  ]

  return (
    <>
      <DataTable
        columns={columns}
        data={snapshots}
        isLoading={isLoading}
        emptyMessage='No snapshots yet.'
      />
      <ConfirmDialog
        open={!!deleting}
        onOpenChange={(open) => !open && setDeleting(null)}
        title='Delete snapshot?'
        desc='The snapshot content is permanently removed from storage.'
        destructive
        confirmText='Delete'
        isLoading={remove.isPending}
        handleConfirm={() => {
          if (!deleting) return
          remove.mutate(deleting.id, {
            onSuccess: () => {
              toast.success('Snapshot deleted')
              setDeleting(null)
            },
            onError: (e) => toast.error(e.message),
          })
        }}
      />
    </>
  )
}
