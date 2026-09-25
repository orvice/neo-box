import { ConnectionStatus } from '@/api/connections'
import { cn } from '@/lib/utils'
import { Badge } from '@/components/ui/badge'

const statusStyle: Record<
  ConnectionStatus,
  { label: string; className: string }
> = {
  [ConnectionStatus.UNSPECIFIED]: {
    label: 'Unknown',
    className: 'bg-muted text-muted-foreground',
  },
  [ConnectionStatus.OK]: {
    label: 'Connected',
    className: 'bg-success-muted text-success-foreground',
  },
  [ConnectionStatus.ERROR]: {
    label: 'Error',
    className: 'bg-danger-muted text-danger-foreground',
  },
}

export function ConnectionStatusBadge({
  status,
  className,
}: {
  status: ConnectionStatus
  className?: string
}) {
  const style = statusStyle[status] ?? statusStyle[ConnectionStatus.UNSPECIFIED]
  return <Badge className={cn(style.className, className)}>{style.label}</Badge>
}
