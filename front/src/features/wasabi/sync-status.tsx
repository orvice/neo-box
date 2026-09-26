import { RefreshCw } from 'lucide-react'
import { toast } from 'sonner'
import { useSyncWasabi, type WasabiSyncState } from '@/api/wasabi'
import { formatTime } from '@/lib/format'
import { Button } from '@/components/ui/button'
import { formatDayLong } from './format'

export function SyncButton({
  connectionId,
  state,
}: {
  connectionId: string
  state: WasabiSyncState | undefined
}) {
  const sync = useSyncWasabi()
  const syncing = state?.syncing || sync.isPending
  return (
    <Button
      size='sm'
      variant='outline'
      disabled={syncing}
      onClick={() =>
        sync.mutate(connectionId, {
          onSuccess: () => toast.success('Sync started'),
          onError: (e) => toast.error(e.message),
        })
      }
    >
      <RefreshCw className={syncing ? 'animate-spin' : undefined} />
      {syncing ? 'Syncing…' : 'Sync now'}
    </Button>
  )
}

/** How far the usage is synced, with the backfill's progress while it runs. */
export function SyncStatus({ state }: { state: WasabiSyncState | undefined }) {
  if (!state) return null
  if (!state.backfillComplete) {
    const pct = Math.round(state.backfillProgress * 100)
    return (
      <div className='max-w-md space-y-1.5'>
        <div className='text-sm'>
          {state.syncing
            ? `Syncing the last 12 months of usage… ${pct}%`
            : `Backfill paused at ${pct}%; it resumes with the next sync.`}
        </div>
        <div
          className='h-1.5 overflow-hidden rounded-full bg-primary/15'
          role='progressbar'
          aria-valuemin={0}
          aria-valuemax={100}
          aria-valuenow={pct}
        >
          <div
            className='h-full rounded-full bg-primary transition-[width]'
            style={{ width: `${pct}%` }}
          />
        </div>
      </div>
    )
  }
  return (
    <div className='text-sm text-muted-foreground'>
      Synced through {formatDayLong(state.lastSyncedDay)} (UTC) · last run{' '}
      {formatTime(state.lastSuccessAt)} · daily at 02:30 UTC
    </div>
  )
}
