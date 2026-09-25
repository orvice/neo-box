import { getRouteApi, Link } from '@tanstack/react-router'
import { ChevronLeft } from 'lucide-react'
import { useConnection } from '@/api/connections'
import { useWasabiBuckets } from '@/api/wasabi'
import { formatBytes } from '@/lib/format'
import { Badge } from '@/components/ui/badge'
import { Page, PageHeader, PageScroll } from '@/components/common/page-parts'
import { formatCompact, formatDayLong } from './format'
import { StatTile } from './stat-tile'
import { UsageCharts } from './usage-charts'

const route = getRouteApi(
  '/_authenticated/connections/$connectionId/buckets/$bucket'
)

/** One bucket's newest totals and daily trends. */
export function WasabiBucketPage() {
  const { connectionId, bucket } = route.useParams()
  const connection = useConnection(connectionId)
  const buckets = useWasabiBuckets(connectionId, true)
  const row = buckets.data?.find((b) => b.name === bucket)
  const latest = row?.latest

  return (
    <Page>
      <PageHeader
        breadcrumb={
          <Link
            to='/connections/$connectionId'
            params={{ connectionId }}
            className='inline-flex items-center gap-1'
          >
            <ChevronLeft className='h-3 w-3' />{' '}
            {connection.data?.name ?? 'Connection'}
          </Link>
        }
        title={
          <span className='inline-flex items-center gap-2'>
            {bucket}
            {row?.deleted && <Badge variant='secondary'>Deleted</Badge>}
          </span>
        }
        subtitle={row?.region}
      />
      <PageScroll className='space-y-6'>
        {latest && (
          <div className='grid gap-4 sm:grid-cols-3'>
            <StatTile
              label='Active storage'
              value={formatBytes(latest.activeStorageBytes)}
              detail={`As of ${formatDayLong(latest.day)}`}
            />
            <StatTile
              label='Deleted, still billed'
              value={formatBytes(latest.deletedStorageBytes)}
              detail={`${formatCompact(latest.billableDeletedObjects)} objects`}
            />
            <StatTile
              label='Objects'
              value={formatCompact(latest.billableObjects)}
            />
          </div>
        )}
        <UsageCharts connectionId={connectionId} bucket={bucket} />
      </PageScroll>
    </Page>
  )
}
