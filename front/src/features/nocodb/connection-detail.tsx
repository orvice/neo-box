import { useState } from 'react'
import { Link } from '@tanstack/react-router'
import { CalendarClock, Camera, ChevronLeft, History } from 'lucide-react'
import { toast } from 'sonner'
import { type Connection } from '@/api/connections'
import {
  isSnapshotActive,
  useCreateSnapshot,
  useNocoDBBases,
  useSnapshots,
  useUpsertBackupPolicy,
  type NocoDBBase,
} from '@/api/nocodb'
import { formatTime } from '@/lib/format'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from '@/components/ui/card'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Switch } from '@/components/ui/switch'
import { Page, PageHeader, PageScroll } from '@/components/common/page-parts'
import { DataTable, type Column } from '@/components/data-table'
import { nocodbBaseUrl } from './format'
import { SnapshotTable } from './snapshot-table'
import { SnapshotStatusBadge } from './status-badge'

/** The detail page of a NocoDB connection: its Bases and snapshots. */
export function NocoDBConnectionDetail({
  connection,
}: {
  connection: Connection
}) {
  const connectionId = connection.id
  const bases = useNocoDBBases(connectionId)
  const snapshots = useSnapshots(connectionId)
  const createSnapshot = useCreateSnapshot()
  const [scheduling, setScheduling] = useState<NocoDBBase | null>(null)
  const [historyBase, setHistoryBase] = useState<NocoDBBase | null>(null)

  function snapshotNow(base: NocoDBBase) {
    createSnapshot.mutate(
      { connectionId, baseId: base.id },
      {
        onSuccess: () => toast.success(`Snapshot of "${base.title}" queued`),
        onError: (e) => toast.error(e.message),
      }
    )
  }

  const columns: Column<NocoDBBase>[] = [
    {
      header: 'Base',
      cell: (b) => (
        <div>
          <div className='font-medium'>{b.title}</div>
          <div className='font-mono text-xs text-muted-foreground'>{b.id}</div>
        </div>
      ),
    },
    {
      header: 'Latest snapshot',
      cell: (b) =>
        b.latestSnapshot ? (
          <div className='space-y-1'>
            <Link
              to='/connections/$connectionId/snapshots/$snapshotId'
              params={{ connectionId, snapshotId: b.latestSnapshot.id }}
            >
              <SnapshotStatusBadge status={b.latestSnapshot.status} />
            </Link>
            <div className='text-xs text-muted-foreground'>
              {isSnapshotActive(b.latestSnapshot)
                ? b.latestSnapshot.progress || 'waiting'
                : formatTime(b.latestSnapshot.createdAt)}
            </div>
          </div>
        ) : (
          <span className='text-xs text-muted-foreground'>Never</span>
        ),
    },
    {
      header: 'Schedule',
      cell: (b) =>
        b.policy?.enabled ? (
          <div className='space-y-1'>
            <Badge variant='secondary' className='font-mono'>
              {b.policy.cron}
            </Badge>
            <div className='text-xs text-muted-foreground'>
              Next {formatTime(b.policy.nextRunAt)}
              {b.policy.retention > 0 && ` · keep ${b.policy.retention}`}
            </div>
          </div>
        ) : (
          <span className='text-xs text-muted-foreground'>Off</span>
        ),
    },
    {
      header: 'Actions',
      cell: (b) => (
        <div className='flex flex-wrap gap-2'>
          <Button
            size='sm'
            onClick={() => snapshotNow(b)}
            disabled={
              isSnapshotActive(b.latestSnapshot) || createSnapshot.isPending
            }
          >
            <Camera />
            Snapshot now
          </Button>
          <Button size='sm' variant='outline' onClick={() => setScheduling(b)}>
            <CalendarClock />
            Schedule
          </Button>
          <Button size='sm' variant='ghost' onClick={() => setHistoryBase(b)}>
            <History />
            History
          </Button>
        </div>
      ),
    },
  ]

  return (
    <Page>
      <PageHeader
        breadcrumb={
          <Link
            to='/connections'
            search={{ provider: 'nocodb' }}
            className='inline-flex items-center gap-1'
          >
            <ChevronLeft className='h-3 w-3' /> NocoDB connections
          </Link>
        }
        title={connection.name}
        subtitle={nocodbBaseUrl(connection)}
      />
      <PageScroll className='space-y-6'>
        <Card>
          <CardHeader>
            <CardTitle>Bases</CardTitle>
            <CardDescription>
              Every Base the API token can see. Snapshots capture tables,
              fields, records, and links.
            </CardDescription>
          </CardHeader>
          <CardContent>
            {bases.error ? (
              <div className='text-sm text-danger-foreground'>
                {bases.error.message}
              </div>
            ) : (
              <DataTable
                columns={columns}
                data={bases.data}
                isLoading={bases.isLoading}
                emptyMessage='No bases visible to this token.'
              />
            )}
          </CardContent>
        </Card>

        <Card>
          <CardHeader>
            <CardTitle>Snapshot history</CardTitle>
            <CardDescription>
              All snapshots taken through this connection.
            </CardDescription>
          </CardHeader>
          <CardContent>
            <SnapshotTable
              snapshots={snapshots.data}
              isLoading={snapshots.isLoading}
            />
          </CardContent>
        </Card>
      </PageScroll>

      {scheduling && (
        <PolicyDialog
          connectionId={connectionId}
          base={scheduling}
          onClose={() => setScheduling(null)}
        />
      )}
      {historyBase && (
        <HistoryDialog
          connectionId={connectionId}
          base={historyBase}
          onClose={() => setHistoryBase(null)}
        />
      )}
    </Page>
  )
}

const cronPresets = [
  { label: 'Hourly', cron: '0 * * * *' },
  { label: 'Daily 03:00', cron: '0 3 * * *' },
  { label: 'Weekly (Sun 03:00)', cron: '0 3 * * 0' },
]

function PolicyDialog({
  connectionId,
  base,
  onClose,
}: {
  connectionId: string
  base: NocoDBBase
  onClose: () => void
}) {
  const upsert = useUpsertBackupPolicy()
  const [enabled, setEnabled] = useState(base.policy?.enabled ?? true)
  const [cron, setCron] = useState(base.policy?.cron || '0 3 * * *')
  const [retention, setRetention] = useState(
    String(base.policy?.retention ?? 7)
  )

  function handleSave() {
    const keep = Number.parseInt(retention || '0', 10)
    if (Number.isNaN(keep) || keep < 0) {
      toast.error('Retention must be 0 or a positive number')
      return
    }
    upsert.mutate(
      {
        connectionId,
        baseId: base.id,
        enabled,
        cron: cron.trim(),
        retention: keep,
      },
      {
        onSuccess: () => {
          toast.success('Schedule saved')
          onClose()
        },
        onError: (e) => toast.error(e.message),
      }
    )
  }

  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Schedule “{base.title}”</DialogTitle>
          <DialogDescription>
            Scheduled snapshots run on the server. Retention only prunes
            scheduled snapshots; manual ones are kept.
          </DialogDescription>
        </DialogHeader>
        <div className='space-y-5'>
          <div className='flex items-center justify-between'>
            <Label htmlFor='policy-enabled'>Enable scheduled snapshots</Label>
            <Switch
              id='policy-enabled'
              checked={enabled}
              onCheckedChange={setEnabled}
            />
          </div>
          <div className='space-y-2'>
            <Label htmlFor='policy-cron'>Cron expression</Label>
            <Input
              id='policy-cron'
              className='font-mono'
              value={cron}
              onChange={(e) => setCron(e.target.value)}
            />
            <div className='flex flex-wrap gap-2'>
              {cronPresets.map((p) => (
                <Button
                  key={p.cron}
                  type='button'
                  size='sm'
                  variant={cron === p.cron ? 'secondary' : 'outline'}
                  onClick={() => setCron(p.cron)}
                >
                  {p.label}
                </Button>
              ))}
            </div>
            <p className='text-xs text-muted-foreground'>
              5-field cron in the server time zone. Prefix with{' '}
              <code>CRON_TZ=Asia/Shanghai</code> to pin a zone.
            </p>
          </div>
          <div className='space-y-2'>
            <Label htmlFor='policy-retention'>Keep last N snapshots</Label>
            <Input
              id='policy-retention'
              type='number'
              min={0}
              value={retention}
              onChange={(e) => setRetention(e.target.value)}
            />
            <p className='text-xs text-muted-foreground'>0 keeps all.</p>
          </div>
        </div>
        <DialogFooter>
          <Button variant='outline' onClick={onClose}>
            Cancel
          </Button>
          <Button onClick={handleSave} disabled={upsert.isPending}>
            Save
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

function HistoryDialog({
  connectionId,
  base,
  onClose,
}: {
  connectionId: string
  base: NocoDBBase
  onClose: () => void
}) {
  const snapshots = useSnapshots(connectionId, base.id)
  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent className='sm:max-w-4xl'>
        <DialogHeader>
          <DialogTitle>Snapshots of “{base.title}”</DialogTitle>
        </DialogHeader>
        <SnapshotTable
          snapshots={snapshots.data}
          isLoading={snapshots.isLoading}
          hideBase
        />
      </DialogContent>
    </Dialog>
  )
}
