import { useId, useState } from 'react'
import { Link } from '@tanstack/react-router'
import {
  useWasabiCostBreakdown,
  type WasabiBucketCost,
  type WasabiCostBreakdown,
} from '@/api/wasabi'
import { formatBytes } from '@/lib/format'
import { cn } from '@/lib/utils'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from '@/components/ui/card'
import { ChartStyle, type ChartConfig } from '@/components/ui/chart'
import { Skeleton } from '@/components/ui/skeleton'
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from '@/components/ui/tooltip'
import { DataTable, type Column } from '@/components/data-table'
import { formatDayLong, formatUSD } from './format'
import { BLUE, NEUTRAL, ORANGE } from './palette'

const partsConfig = {
  active: { label: 'Active storage', theme: BLUE },
  deleted: { label: 'Deleted, still billed', theme: ORANGE },
  minimum: { label: '1 TB minimum', theme: NEUTRAL },
} satisfies ChartConfig

type Part = keyof typeof partsConfig

const TOP_BUCKETS = 10

/**
 * Where the cost estimate's period goes: what the charge pays for, with
 * deleted-but-billed data called out, and how it splits by bucket.
 */
export function CostBreakdownCard({ connectionId }: { connectionId: string }) {
  const breakdown = useWasabiCostBreakdown(connectionId)
  const b = breakdown.data
  const chartId = `wasabi-cost-${useId().replace(/:/g, '')}`

  return (
    <Card>
      <CardHeader>
        <CardTitle>Where the cost goes</CardTitle>
        <CardDescription>
          {b && b.daysWithData > 0
            ? `${periodLabel(b)} · ${formatUSD(total(b))} so far at ${formatUSD(b.pricePerTbMonth)}/TB-month`
            : 'Shown once this period has synced usage.'}
        </CardDescription>
      </CardHeader>
      <CardContent data-chart={chartId} className='space-y-6'>
        <ChartStyle id={chartId} config={partsConfig} />
        {breakdown.isLoading ? (
          <Skeleton className='h-24' />
        ) : breakdown.error ? (
          <div className='text-sm text-danger-foreground'>
            {breakdown.error.message}
          </div>
        ) : b && b.daysWithData > 0 ? (
          <>
            <Composition breakdown={b} />
            <BucketCosts breakdown={b} connectionId={connectionId} />
          </>
        ) : null}
      </CardContent>
    </Card>
  )
}

function total(b: WasabiCostBreakdown) {
  return b.activeCost + b.deletedCost + b.minimumCost
}

function periodLabel(b: WasabiCostBreakdown) {
  return b.rolling
    ? `Last 30 days through ${formatDayLong(b.dataThrough)}`
    : `Cycle ${formatDayLong(b.periodStart)} – ${formatDayLong(b.periodEnd)}`
}

function pct(part: number, whole: number) {
  if (!(whole > 0)) return '0%'
  const v = (part / whole) * 100
  return v > 0 && v < 1 ? '<1%' : `${Math.round(v)}%`
}

/** One stacked bar of the period's charge, with a legend that holds the
 *  exact values (it doubles as the bar's table view). */
function Composition({ breakdown: b }: { breakdown: WasabiCostBreakdown }) {
  const sum = total(b)
  const parts = (
    [
      ['active', b.activeCost],
      ['deleted', b.deletedCost],
      ['minimum', b.minimumCost],
    ] as [Part, number][]
  ).filter(([, v]) => v > 0)
  const unattributed = b.unattributedCost
  const showUnattributed = Math.abs(unattributed) >= Math.max(sum * 0.005, 0.01)

  return (
    <div className='space-y-3'>
      {b.deletedCost > 0 && (
        <p className='text-sm'>
          <span className='font-semibold tabular-nums'>
            {formatUSD(b.deletedCost)} ({pct(b.deletedCost, sum)})
          </span>{' '}
          of this period pays for deleted data that is still billed. Wasabi
          charges for objects deleted before they are 90 days old until they
          reach 90 days.
        </p>
      )}
      <div
        className='flex h-4 w-full gap-[2px]'
        role='img'
        aria-label='Cost by what it pays for'
      >
        {parts.map(([key, value], i) => (
          <Tooltip key={key}>
            <TooltipTrigger asChild>
              <div
                className={cn(
                  'h-full min-w-[2px]',
                  i === 0 && 'rounded-l-[4px]',
                  i === parts.length - 1 && 'rounded-r-[4px]'
                )}
                style={{
                  flexGrow: value,
                  flexBasis: 0,
                  background: `var(--color-${key})`,
                }}
              />
            </TooltipTrigger>
            <TooltipContent>
              <span className='font-medium tabular-nums'>
                {formatUSD(value)}
              </span>{' '}
              {partsConfig[key].label} · {pct(value, sum)}
            </TooltipContent>
          </Tooltip>
        ))}
      </div>
      <ul className='flex flex-wrap gap-x-6 gap-y-2 text-sm'>
        {parts.map(([key, value]) => (
          <li key={key} className='flex items-center gap-2'>
            <span
              className='h-3 w-1 shrink-0 rounded-[2px]'
              style={{ background: `var(--color-${key})` }}
            />
            <span className='font-medium tabular-nums'>{formatUSD(value)}</span>
            <span className='text-muted-foreground'>
              {partsConfig[key].label} · {pct(value, sum)}
            </span>
          </li>
        ))}
      </ul>
      {showUnattributed && (
        <p className='text-xs text-muted-foreground'>
          {unattributed > 0
            ? `${formatUSD(unattributed)} of active and deleted storage isn't in any bucket's figures.`
            : `Bucket figures add up to ${formatUSD(-unattributed)} more than the account's.`}
        </p>
      )}
    </div>
  )
}

function BucketCosts({
  breakdown: b,
  connectionId,
}: {
  breakdown: WasabiCostBreakdown
  connectionId: string
}) {
  const [showAll, setShowAll] = useState(false)
  const sum = total(b)
  const max = Math.max(...b.buckets.map((c) => c.activeCost + c.deletedCost), 0)
  const rows = showAll ? b.buckets : b.buckets.slice(0, TOP_BUCKETS)

  const columns: Column<WasabiBucketCost>[] = [
    {
      header: 'Bucket',
      cell: (c) => (
        <div className='flex items-center gap-2'>
          <Link
            to='/connections/$connectionId/buckets/$bucket'
            params={{ connectionId, bucket: c.bucket }}
            className='font-medium hover:underline'
          >
            {c.bucket}
          </Link>
          {c.deleted && <Badge variant='secondary'>Deleted</Badge>}
        </div>
      ),
    },
    {
      header: 'Share',
      cell: (c) => {
        const value = c.activeCost + c.deletedCost
        return (
          <div className='flex min-w-36 items-center gap-2'>
            <div className='h-1.5 flex-1 rounded-full bg-muted'>
              <div
                className='h-full rounded-full'
                style={{
                  width: `${max > 0 ? (value / max) * 100 : 0}%`,
                  background: 'var(--color-active)',
                }}
              />
            </div>
            <span className='w-10 text-end text-xs tabular-nums'>
              {pct(value, sum)}
            </span>
          </div>
        )
      },
    },
    {
      header: 'Total',
      cell: (c) => (
        <span className='font-medium tabular-nums'>
          {formatUSD(c.activeCost + c.deletedCost)}
        </span>
      ),
    },
    {
      header: 'Active',
      cell: (c) => (
        <span className='tabular-nums'>{formatUSD(c.activeCost)}</span>
      ),
    },
    {
      header: 'Deleted, still billed',
      cell: (c) => (
        <span className='tabular-nums'>
          {formatUSD(c.deletedCost)}
          {c.deletedCost > 0 && (
            <span className='ms-1 text-xs text-muted-foreground'>
              {pct(c.deletedCost, c.activeCost + c.deletedCost)} of bucket
            </span>
          )}
        </span>
      ),
    },
    {
      header: 'Deleted this period',
      cell: (c) => (
        <span className='tabular-nums'>
          {formatBytes(c.deletedInPeriodBytes)}
        </span>
      ),
    },
  ]

  return (
    <div className='space-y-2'>
      <DataTable
        columns={columns}
        data={rows}
        isLoading={false}
        emptyMessage='No bucket figures in this period.'
      />
      {b.buckets.length > TOP_BUCKETS && (
        <Button variant='ghost' size='sm' onClick={() => setShowAll((v) => !v)}>
          {showAll
            ? 'Show top buckets'
            : `Show all ${b.buckets.length} buckets`}
        </Button>
      )}
    </div>
  )
}
