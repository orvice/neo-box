import { useState } from 'react'
import { Link, useNavigate, useParams } from '@tanstack/react-router'
import { type JsonValue } from '@bufbuild/protobuf'
import {
  ChevronLeft,
  ChevronRight,
  Download,
  Table2,
  Trash2,
} from 'lucide-react'
import { toast } from 'sonner'
import {
  SnapshotStatus,
  downloadSnapshot,
  useDeleteSnapshot,
  useSnapshot,
  useSnapshotRecords,
} from '@/api/nocodb'
import { cn } from '@/lib/utils'
import { Button } from '@/components/ui/button'
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from '@/components/ui/card'
import { Skeleton } from '@/components/ui/skeleton'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import { Page, PageHeader, PageScroll } from '@/components/common/page-parts'
import { ConfirmDialog } from '@/components/confirm-dialog'
import { EmptyState } from '@/components/empty-state'
import {
  formatBytes,
  formatCount,
  formatDuration,
  formatTime,
  triggerLabel,
} from './format'
import { SnapshotStatusBadge } from './status-badge'

const PAGE_SIZE = 50

export function SnapshotDetailPage() {
  const { snapshotId } = useParams({
    from: '/_authenticated/nocodb/snapshots/$snapshotId',
  })
  const navigate = useNavigate()
  const { data: snap, isLoading, error } = useSnapshot(snapshotId)
  const remove = useDeleteSnapshot()
  const [confirmDelete, setConfirmDelete] = useState(false)
  const [tableId, setTableId] = useState<string>()
  const activeTableId = tableId ?? snap?.tables[0]?.id

  const back = snap ? (
    <Link
      to='/nocodb/$connectionId'
      params={{ connectionId: snap.connectionId }}
      className='inline-flex items-center gap-1'
    >
      <ChevronLeft className='h-3 w-3' /> Back to bases
    </Link>
  ) : (
    <Link to='/nocodb' className='inline-flex items-center gap-1'>
      <ChevronLeft className='h-3 w-3' /> NocoDB
    </Link>
  )

  if (isLoading) {
    return (
      <Page>
        <PageHeader title='Snapshot' breadcrumb={back} />
        <Skeleton className='h-40' />
      </Page>
    )
  }
  if (error || !snap) {
    return (
      <Page>
        <PageHeader title='Snapshot' breadcrumb={back} />
        <EmptyState
          title='Snapshot not found'
          description={error?.message ?? 'It may have been deleted.'}
        />
      </Page>
    )
  }

  const succeeded = snap.status === SnapshotStatus.SUCCEEDED

  return (
    <Page>
      <PageHeader
        breadcrumb={back}
        title={snap.baseTitle || snap.baseId}
        subtitle={`${triggerLabel(snap.trigger)} snapshot · ${formatTime(snap.createdAt)}`}
        actions={
          <>
            <Button
              size='sm'
              disabled={!succeeded}
              onClick={() =>
                downloadSnapshot(snap.id).catch((e: Error) =>
                  toast.error(e.message)
                )
              }
            >
              <Download />
              Download JSON
            </Button>
            <Button
              size='sm'
              variant='outline'
              disabled={!succeeded && snap.status !== SnapshotStatus.FAILED}
              onClick={() => setConfirmDelete(true)}
            >
              <Trash2 />
              Delete
            </Button>
          </>
        }
      />
      <PageScroll className='space-y-6'>
        <div className='grid gap-4 sm:grid-cols-2 lg:grid-cols-4'>
          <Stat label='Status'>
            <SnapshotStatusBadge status={snap.status} />
          </Stat>
          <Stat label='Records'>{formatCount(snap.recordCount)}</Stat>
          <Stat label='Links'>{formatCount(snap.linkCount)}</Stat>
          <Stat label='Size · Duration'>
            {succeeded
              ? `${formatBytes(snap.sizeBytes)} · ${formatDuration(snap)}`
              : '-'}
          </Stat>
        </div>

        {snap.progress && (
          <Card>
            <CardContent className='py-4 text-sm text-muted-foreground'>
              {snap.progress}
            </CardContent>
          </Card>
        )}
        {snap.status === SnapshotStatus.FAILED && (
          <Card className='border-danger-foreground/30'>
            <CardHeader>
              <CardTitle className='text-danger-foreground'>Failed</CardTitle>
              <CardDescription className='font-mono text-xs break-all'>
                {snap.error}
              </CardDescription>
            </CardHeader>
          </Card>
        )}

        {succeeded && (
          <div className='grid gap-6 lg:grid-cols-[240px_minmax(0,1fr)]'>
            <Card className='h-fit'>
              <CardHeader>
                <CardTitle>Tables</CardTitle>
              </CardHeader>
              <CardContent className='space-y-1 px-2'>
                {snap.tables.map((t) => (
                  <button
                    key={t.id}
                    type='button'
                    onClick={() => setTableId(t.id)}
                    className={cn(
                      'flex w-full items-center gap-2 rounded-md px-3 py-2 text-start text-sm hover:bg-muted',
                      activeTableId === t.id && 'bg-muted font-medium'
                    )}
                  >
                    <Table2 className='h-4 w-4 shrink-0 text-muted-foreground' />
                    <span className='min-w-0 flex-1 truncate'>{t.title}</span>
                    <span className='text-xs text-muted-foreground'>
                      {formatCount(t.recordCount)}
                    </span>
                  </button>
                ))}
                {snap.tables.length === 0 && (
                  <div className='px-3 text-sm text-muted-foreground'>
                    This base has no tables.
                  </div>
                )}
              </CardContent>
            </Card>
            {activeTableId && (
              <RecordsCard
                key={activeTableId}
                snapshotId={snap.id}
                tableId={activeTableId}
                title={
                  snap.tables.find((t) => t.id === activeTableId)?.title ?? ''
                }
              />
            )}
          </div>
        )}
      </PageScroll>

      <ConfirmDialog
        open={confirmDelete}
        onOpenChange={setConfirmDelete}
        title='Delete snapshot?'
        desc='The snapshot content is permanently removed from storage.'
        destructive
        confirmText='Delete'
        isLoading={remove.isPending}
        handleConfirm={() =>
          remove.mutate(snap.id, {
            onSuccess: () => {
              toast.success('Snapshot deleted')
              navigate({
                to: '/nocodb/$connectionId',
                params: { connectionId: snap.connectionId },
              })
            },
            onError: (e) => toast.error(e.message),
          })
        }
      />
    </Page>
  )
}

function Stat({
  label,
  children,
}: {
  label: string
  children: React.ReactNode
}) {
  return (
    <Card>
      <CardContent className='space-y-1 py-4'>
        <div className='text-xs text-muted-foreground'>{label}</div>
        <div className='text-lg font-semibold'>{children}</div>
      </CardContent>
    </Card>
  )
}

function RecordsCard({
  snapshotId,
  tableId,
  title,
}: {
  snapshotId: string
  tableId: string
  title: string
}) {
  const [page, setPage] = useState(1)
  const { data, isLoading, isFetching, error } = useSnapshotRecords(
    snapshotId,
    tableId,
    page,
    PAGE_SIZE
  )
  const total = Number(data?.total ?? 0)
  const pages = Math.max(1, Math.ceil(total / PAGE_SIZE))

  return (
    <Card className='min-w-0'>
      <CardHeader>
        <CardTitle>{title}</CardTitle>
        <CardDescription>
          {formatCount(total)} records · {data?.fields.length ?? 0} fields
        </CardDescription>
      </CardHeader>
      <CardContent className='space-y-4'>
        {isLoading ? (
          <Skeleton className='h-64' />
        ) : error ? (
          <div className='text-sm text-danger-foreground'>{error.message}</div>
        ) : !data || data.records.length === 0 ? (
          <EmptyState title='No records' />
        ) : (
          <div
            className={cn(
              'overflow-x-auto rounded-md border',
              isFetching && 'opacity-70'
            )}
          >
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead className='w-16'>ID</TableHead>
                  {data.fields.map((f) => (
                    <TableHead key={f.id} className='whitespace-nowrap'>
                      {f.title}
                      <span className='ms-1 text-[10px] font-normal text-muted-foreground'>
                        {f.type}
                      </span>
                    </TableHead>
                  ))}
                </TableRow>
              </TableHeader>
              <TableBody>
                {data.records.map((r, i) => {
                  const rec = r as {
                    id?: JsonValue
                    fields?: Record<string, JsonValue>
                  }
                  return (
                    <TableRow key={i}>
                      <TableCell className='font-mono text-xs'>
                        {renderValue(rec.id)}
                      </TableCell>
                      {data.fields.map((f) => (
                        <TableCell
                          key={f.id}
                          className='max-w-64 truncate text-sm'
                          title={renderValue(rec.fields?.[f.title])}
                        >
                          {renderValue(rec.fields?.[f.title])}
                        </TableCell>
                      ))}
                    </TableRow>
                  )
                })}
              </TableBody>
            </Table>
          </div>
        )}
        <div className='flex items-center justify-end gap-2 text-sm'>
          <span className='text-muted-foreground'>
            Page {page} of {pages}
          </span>
          <Button
            size='sm'
            variant='outline'
            disabled={page <= 1}
            onClick={() => setPage((p) => p - 1)}
          >
            <ChevronLeft />
          </Button>
          <Button
            size='sm'
            variant='outline'
            disabled={page >= pages}
            onClick={() => setPage((p) => p + 1)}
          >
            <ChevronRight />
          </Button>
        </div>
      </CardContent>
    </Card>
  )
}

function renderValue(v: JsonValue | undefined): string {
  if (v === undefined || v === null) return ''
  if (typeof v === 'string') return v
  if (typeof v === 'number' || typeof v === 'boolean') return String(v)
  if (Array.isArray(v)) {
    // Linked records and attachments: show display values when present.
    return v
      .map((item) => {
        if (item && typeof item === 'object' && !Array.isArray(item)) {
          const o = item as Record<string, JsonValue>
          if (typeof o.title === 'string') return o.title
          const fields = o.fields as Record<string, JsonValue> | undefined
          const first = fields && Object.values(fields)[0]
          if (first !== undefined) return renderValue(first)
          if (o.id !== undefined) return `#${renderValue(o.id)}`
        }
        return renderValue(item)
      })
      .join(', ')
  }
  return JSON.stringify(v)
}
