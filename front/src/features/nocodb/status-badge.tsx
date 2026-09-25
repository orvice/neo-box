import { Loader2 } from 'lucide-react'
import { SnapshotStatus } from '@/api/nocodb'
import { cn } from '@/lib/utils'
import { Badge } from '@/components/ui/badge'

const statusStyle: Record<
  SnapshotStatus,
  { label: string; className: string }
> = {
  [SnapshotStatus.UNSPECIFIED]: {
    label: 'Unknown',
    className: 'bg-muted text-muted-foreground',
  },
  [SnapshotStatus.PENDING]: {
    label: 'Queued',
    className: 'bg-running-muted text-running-foreground',
  },
  [SnapshotStatus.RUNNING]: {
    label: 'Running',
    className: 'bg-running-muted text-running-foreground',
  },
  [SnapshotStatus.SUCCEEDED]: {
    label: 'Succeeded',
    className: 'bg-success-muted text-success-foreground',
  },
  [SnapshotStatus.FAILED]: {
    label: 'Failed',
    className: 'bg-danger-muted text-danger-foreground',
  },
}

export function SnapshotStatusBadge({ status }: { status: SnapshotStatus }) {
  const style = statusStyle[status] ?? statusStyle[SnapshotStatus.UNSPECIFIED]
  const active =
    status === SnapshotStatus.PENDING || status === SnapshotStatus.RUNNING
  return (
    <Badge className={cn('gap-1', style.className)}>
      {active && <Loader2 className='h-3 w-3 animate-spin' />}
      {style.label}
    </Badge>
  )
}
