import { timestampDate } from '@bufbuild/protobuf/wkt'
import { ExternalLink } from 'lucide-react'
import { ConnectionStatus, type Connection } from '@/api/connections'
import { useNocoDBBases } from '@/api/nocodb'
import { formatTime } from '@/lib/format'
import { Skeleton } from '@/components/ui/skeleton'
import { nocodbBaseUrl } from './format'

/** The instance URL, Base count, and the newest snapshot. */
export function NocoDBConnectionSummary({
  connection,
}: {
  connection: Connection
}) {
  const baseUrl = nocodbBaseUrl(connection)
  // A failing connection already shows its error; listing Bases would
  // only spin through the client's retries.
  const failing = connection.status === ConnectionStatus.ERROR
  const bases = useNocoDBBases(connection.id, !failing)

  const latest = bases.data
    ?.map((b) => b.latestSnapshot)
    .filter((s) => s?.createdAt)
    .sort(
      (a, b) =>
        timestampDate(b!.createdAt!).getTime() -
        timestampDate(a!.createdAt!).getTime()
    )[0]

  return (
    <div className='space-y-1 text-sm'>
      <a
        href={baseUrl}
        target='_blank'
        rel='noreferrer'
        className='inline-flex items-center gap-1 break-all text-muted-foreground hover:underline'
      >
        {baseUrl}
        <ExternalLink className='h-3 w-3' />
      </a>
      {failing ? null : bases.isLoading ? (
        <Skeleton className='h-4 w-48' />
      ) : bases.error ? (
        <div className='text-xs text-muted-foreground'>
          Bases unavailable: {bases.error.message}
        </div>
      ) : (
        <div>
          {bases.data?.length ?? 0} base(s) · last snapshot{' '}
          {latest ? formatTime(latest.createdAt) : 'never'}
        </div>
      )}
    </div>
  )
}
