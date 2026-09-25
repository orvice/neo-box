import { ConnectionStatus, type Connection } from '@/api/connections'
import { useWasabiOverview } from '@/api/wasabi'
import { formatBytes } from '@/lib/format'
import { Skeleton } from '@/components/ui/skeleton'
import { formatDayLong, formatUSD, wasabiConfig } from './format'

/** Active storage and the period's estimate, from the newest synced day. */
export function WasabiConnectionSummary({
  connection,
}: {
  connection: Connection
}) {
  const failing = connection.status === ConnectionStatus.ERROR
  const overview = useWasabiOverview(connection.id, !failing)
  const cfg = wasabiConfig(connection)

  return (
    <div className='space-y-1 text-sm'>
      <div className='font-mono text-xs text-muted-foreground'>
        {cfg?.accessKeyId}
      </div>
      {failing ? null : overview.isLoading ? (
        <Skeleton className='h-4 w-48' />
      ) : overview.error ? (
        <div className='text-xs text-muted-foreground'>
          Usage unavailable: {overview.error.message}
        </div>
      ) : !overview.data?.latest ? (
        <div className='text-xs text-muted-foreground'>
          {overview.data?.sync?.syncing
            ? 'Syncing the last 12 months…'
            : 'No usage synced yet'}
        </div>
      ) : (
        <div>
          {formatBytes(overview.data.latest.activeStorageBytes)} stored
          {!!overview.data.costEstimate?.daysWithData && (
            <> · est. {formatUSD(overview.data.costEstimate.projectedCost)}</>
          )}
          <span className='text-muted-foreground'>
            {' '}
            · as of {formatDayLong(overview.data.latest.day)}
          </span>
        </div>
      )}
    </div>
  )
}
