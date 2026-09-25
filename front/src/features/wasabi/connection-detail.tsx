import { useState } from 'react'
import { Link } from '@tanstack/react-router'
import { ChevronLeft, TriangleAlert } from 'lucide-react'
import { type Connection } from '@/api/connections'
import {
  useWasabiBuckets,
  useWasabiOverview,
  type WasabiBucket,
  type WasabiCostEstimate,
} from '@/api/wasabi'
import { formatBytes } from '@/lib/format'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from '@/components/ui/card'
import { Label } from '@/components/ui/label'
import { Skeleton } from '@/components/ui/skeleton'
import { Switch } from '@/components/ui/switch'
import { Page, PageHeader, PageScroll } from '@/components/common/page-parts'
import { DataTable, type Column } from '@/components/data-table'
import { formatCompact, formatDayLong, formatUSD, wasabiConfig } from './format'
import { StatTile } from './stat-tile'
import { SyncButton, SyncStatus } from './sync-status'
import { UsageCharts } from './usage-charts'

/** The detail page of a Wasabi connection: totals, cost, trends, buckets. */
export function WasabiConnectionDetail({
  connection,
}: {
  connection: Connection
}) {
  const overview = useWasabiOverview(connection.id)
  const data = overview.data
  const latest = data?.latest
  const estimate = data?.costEstimate

  return (
    <Page>
      <PageHeader
        breadcrumb={
          <Link
            to='/connections'
            search={{ provider: 'wasabi' }}
            className='inline-flex items-center gap-1'
          >
            <ChevronLeft className='h-3 w-3' /> Wasabi connections
          </Link>
        }
        title={connection.name}
        subtitle={
          <span className='font-mono'>
            {wasabiConfig(connection)?.accessKeyId}
          </span>
        }
        actions={<SyncButton connectionId={connection.id} state={data?.sync} />}
      />
      <PageScroll className='space-y-6'>
        <SyncStatus state={data?.sync} />

        {overview.isLoading ? (
          <Skeleton className='h-28' />
        ) : overview.error ? (
          <div className='text-sm text-danger-foreground'>
            {overview.error.message}
          </div>
        ) : latest ? (
          <div className='grid gap-4 sm:grid-cols-2 xl:grid-cols-4'>
            <StatTile
              label='Active storage'
              value={formatBytes(latest.activeStorageBytes)}
              detail={`As of ${formatDayLong(latest.day)}`}
            />
            <StatTile
              label='Deleted, still billed'
              value={formatBytes(latest.deletedStorageBytes)}
              detail={`${formatCompact(latest.billableDeletedObjects)} objects under the minimum storage duration`}
            />
            <StatTile
              label='Objects'
              value={formatCompact(latest.billableObjects)}
              detail={`${data?.bucketCount ?? 0} bucket(s)`}
            />
            {estimate && <CostTile estimate={estimate} />}
          </div>
        ) : null}

        {estimate?.egressExceedsStorage && latest && (
          <Alert>
            <TriangleAlert />
            <AlertTitle>Egress above stored volume</AlertTitle>
            <AlertDescription>
              {formatBytes(estimate.egressBytes)} was downloaded in this period
              against {formatBytes(latest.activeStorageBytes)} stored.
              Wasabi&apos;s free egress covers up to the stored volume; heavier
              use can lead to charges or a plan change.
            </AlertDescription>
          </Alert>
        )}

        <UsageCharts connectionId={connection.id} bucket='' />
        <BucketsCard connectionId={connection.id} />
      </PageScroll>
    </Page>
  )
}

function CostTile({ estimate }: { estimate: WasabiCostEstimate }) {
  // A backfill fills the oldest days first; until it reaches this period
  // there is nothing to estimate from.
  if (estimate.daysWithData === 0) {
    return (
      <StatTile
        label='Estimated cost'
        value='—'
        detail='Shown once this period has synced usage.'
      />
    )
  }
  const period = estimate.rolling
    ? 'monthly run rate from the newest day'
    : `cycle ${formatDayLong(estimate.periodStart)} – ${formatDayLong(estimate.periodEnd)}`
  return (
    <StatTile
      label='Estimated cost'
      value={formatUSD(estimate.projectedCost)}
      detail={`${formatUSD(estimate.costToDate)} so far · ${period} · at ${formatUSD(estimate.pricePerTbMonth)}/TB-month`}
    />
  )
}

function BucketsCard({ connectionId }: { connectionId: string }) {
  const [showDeleted, setShowDeleted] = useState(false)
  const buckets = useWasabiBuckets(connectionId, showDeleted)

  const columns: Column<WasabiBucket>[] = [
    {
      header: 'Bucket',
      cell: (b) => (
        <div className='flex items-center gap-2'>
          <Link
            to='/connections/$connectionId/buckets/$bucket'
            params={{ connectionId, bucket: b.name }}
            className='font-medium hover:underline'
          >
            {b.name}
          </Link>
          {b.deleted && <Badge variant='secondary'>Deleted</Badge>}
        </div>
      ),
    },
    { header: 'Region', cell: (b) => b.region || '-' },
    {
      header: 'Active storage',
      cell: (b) => (
        <span className='tabular-nums'>
          {formatBytes(b.latest?.activeStorageBytes ?? 0n)}
        </span>
      ),
    },
    {
      header: 'Deleted, still billed',
      cell: (b) => (
        <span className='tabular-nums'>
          {formatBytes(b.latest?.deletedStorageBytes ?? 0n)}
        </span>
      ),
    },
    {
      header: 'Objects',
      cell: (b) => (
        <span className='tabular-nums'>
          {formatCompact(b.latest?.billableObjects ?? 0n)}
        </span>
      ),
    },
    {
      header: 'As of',
      cell: (b) => (
        <span className='text-xs text-muted-foreground'>
          {formatDayLong(b.latest?.day ?? '')}
        </span>
      ),
    },
  ]

  return (
    <Card>
      <CardHeader className='flex flex-row items-start justify-between gap-4'>
        <div className='space-y-1.5'>
          <CardTitle>Buckets</CardTitle>
          <CardDescription>
            From the newest day of data. A bucket missing from it is shown as
            deleted.
          </CardDescription>
        </div>
        <div className='flex items-center gap-2'>
          <Switch
            id='show-deleted'
            checked={showDeleted}
            onCheckedChange={setShowDeleted}
          />
          <Label htmlFor='show-deleted' className='text-xs'>
            Show deleted
          </Label>
        </div>
      </CardHeader>
      <CardContent>
        <DataTable
          columns={columns}
          data={buckets.data}
          isLoading={buckets.isLoading}
          emptyMessage='No buckets synced yet.'
        />
      </CardContent>
    </Card>
  )
}
