import { useMemo, useState, type ReactNode } from 'react'
import { Area, AreaChart, CartesianGrid, XAxis, YAxis } from 'recharts'
import { useWasabiUsage, type WasabiUsage } from '@/api/wasabi'
import { formatBytes, formatCount } from '@/lib/format'
import { cn } from '@/lib/utils'
import { Button } from '@/components/ui/button'
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from '@/components/ui/card'
import {
  ChartContainer,
  ChartLegend,
  ChartLegendContent,
  ChartTooltip,
  ChartTooltipContent,
  type ChartConfig,
} from '@/components/ui/chart'
import { Skeleton } from '@/components/ui/skeleton'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs'
import {
  addDays,
  byteTicks,
  formatBytesTick,
  formatCompact,
  formatDayLong,
  formatDayShort,
  todayUTC,
} from './format'

// Categorical slots 1 and 2 of the reference palette, validated on this
// app's card surfaces (#ffffff light, #020919 dark).
const BLUE = { light: '#2a78d6', dark: '#3987e5' }
const ORANGE = { light: '#eb6834', dark: '#d95926' }

const storageConfig = {
  active: { label: 'Active', theme: BLUE },
  deleted: { label: 'Deleted, still billed', theme: ORANGE },
} satisfies ChartConfig

const downloadConfig = {
  download: { label: 'Downloaded', theme: BLUE },
} satisfies ChartConfig

const callsConfig = {
  calls: { label: 'API calls', theme: BLUE },
} satisfies ChartConfig

const RANGES = [
  { days: 30, label: '30 days' },
  { days: 90, label: '90 days' },
  { days: 365, label: '1 year' },
] as const

type Row = {
  day: string
  active: number
  deleted: number
  download: number
  calls: number
}

function toRows(days: WasabiUsage[] | undefined): Row[] {
  return (days ?? []).map((d) => ({
    day: d.day,
    active: Number(d.activeStorageBytes),
    deleted: Number(d.deletedStorageBytes),
    download: Number(d.downloadBytes),
    calls: Number(d.apiCalls),
  }))
}

/**
 * The daily usage of the account (bucket '') or one bucket: a range picker
 * scoping three charts. Days are UTC.
 */
export function UsageCharts({
  connectionId,
  bucket,
}: {
  connectionId: string
  bucket: string
}) {
  const [days, setDays] = useState<number>(90)
  const to = todayUTC()
  const from = addDays(to, -(days - 1))
  const usage = useWasabiUsage(connectionId, bucket, from, to)
  const rows = useMemo(() => toRows(usage.data), [usage.data])
  const storageTicks = byteTicks(
    Math.max(0, ...rows.map((r) => r.active + r.deleted))
  )
  const downloadTicks = byteTicks(Math.max(0, ...rows.map((r) => r.download)))
  // A new range keeps the previous charts on screen, dimmed.
  const stale = usage.isPlaceholderData

  return (
    <section className='space-y-4'>
      <div className='flex flex-wrap items-center gap-2'>
        {RANGES.map((r) => (
          <Button
            key={r.days}
            size='sm'
            variant={days === r.days ? 'secondary' : 'ghost'}
            aria-pressed={days === r.days}
            onClick={() => setDays(r.days)}
          >
            {r.label}
          </Button>
        ))}
        <span className='text-xs text-muted-foreground'>
          {formatDayLong(from)} – {formatDayLong(to)} (UTC)
        </span>
      </div>

      {usage.isLoading ? (
        <Skeleton className='h-72' />
      ) : usage.error ? (
        <div className='text-sm text-danger-foreground'>
          {usage.error.message}
        </div>
      ) : !rows.length ? (
        <Card>
          <CardContent className='py-10 text-center text-sm text-muted-foreground'>
            No usage in this range yet.
          </CardContent>
        </Card>
      ) : (
        <div
          className={cn('space-y-4 transition-opacity', stale && 'opacity-60')}
        >
          <ChartCard
            title='Stored data'
            description='Active storage and deleted data still billed under the minimum storage duration, at midnight UTC.'
            rows={rows}
            columns={[
              { key: 'active', label: 'Active', format: formatBytes },
              { key: 'deleted', label: 'Deleted', format: formatBytes },
            ]}
          >
            <ChartContainer config={storageConfig} className='h-64 w-full'>
              <AreaChart data={rows} margin={{ top: 8, right: 12, left: 4 }}>
                <ChartGrid />
                {dayAxis}
                <YAxis
                  {...yAxisProps}
                  ticks={storageTicks}
                  domain={[0, storageTicks[storageTicks.length - 1]]}
                  tickFormatter={formatBytesTick}
                />
                <ChartTooltip
                  content={
                    <Tooltip
                      config={storageConfig}
                      format={(v) => formatBytes(v)}
                    />
                  }
                />
                <Area {...areaProps('active')} stackId='storage' />
                <Area {...areaProps('deleted')} stackId='storage' />
                <ChartLegend content={<ChartLegendContent />} />
              </AreaChart>
            </ChartContainer>
          </ChartCard>

          <div className='grid gap-4 lg:grid-cols-2'>
            <ChartCard
              title='Downloaded (egress)'
              description='Bytes downloaded per day. Free while monthly egress stays within the stored volume.'
              rows={rows}
              columns={[
                { key: 'download', label: 'Downloaded', format: formatBytes },
              ]}
            >
              <ChartContainer config={downloadConfig} className='h-52 w-full'>
                <AreaChart data={rows} margin={{ top: 8, right: 12, left: 4 }}>
                  <ChartGrid />
                  {dayAxis}
                  <YAxis
                    {...yAxisProps}
                    ticks={downloadTicks}
                    domain={[0, downloadTicks[downloadTicks.length - 1]]}
                    tickFormatter={formatBytesTick}
                  />
                  <ChartTooltip
                    content={
                      <Tooltip
                        config={downloadConfig}
                        format={(v) => formatBytes(v)}
                      />
                    }
                  />
                  <Area {...areaProps('download')} />
                </AreaChart>
              </ChartContainer>
            </ChartCard>

            <ChartCard
              title='API calls'
              description='Requests per day, all methods.'
              rows={rows}
              columns={[
                { key: 'calls', label: 'API calls', format: formatCount },
              ]}
            >
              <ChartContainer config={callsConfig} className='h-52 w-full'>
                <AreaChart data={rows} margin={{ top: 8, right: 12, left: 4 }}>
                  <ChartGrid />
                  {dayAxis}
                  <YAxis
                    {...yAxisProps}
                    tickFormatter={(v) => formatCompact(v)}
                  />
                  <ChartTooltip
                    content={
                      <Tooltip
                        config={callsConfig}
                        format={(v) => formatCount(v)}
                      />
                    }
                  />
                  <Area {...areaProps('calls')} />
                </AreaChart>
              </ChartContainer>
            </ChartCard>
          </div>
        </div>
      )}
    </section>
  )
}

// --- chart parts ---

function ChartGrid() {
  // Solid hairlines, horizontal only.
  return <CartesianGrid vertical={false} />
}

const dayAxis = (
  <XAxis
    dataKey='day'
    tickLine={false}
    axisLine={false}
    tickMargin={8}
    minTickGap={40}
    tickFormatter={formatDayShort}
  />
)

const yAxisProps = {
  tickLine: false,
  axisLine: false,
  width: 72,
  className: 'tabular-nums',
} as const

function areaProps(key: string) {
  return {
    dataKey: key,
    type: 'linear' as const,
    stroke: `var(--color-${key})`,
    strokeWidth: 2,
    fill: `var(--color-${key})`,
    fillOpacity: 0.1,
    isAnimationActive: false,
    activeDot: { r: 4, strokeWidth: 2, stroke: 'var(--card)' },
  }
}

/** One row per series: a line key, the value (strong), then the label. */
function Tooltip(
  props: React.ComponentProps<typeof ChartTooltipContent> & {
    config: ChartConfig
    format: (v: number) => string
  }
) {
  const { config, format, ...rest } = props
  return (
    <ChartTooltipContent
      {...rest}
      labelFormatter={(value) => formatDayLong(String(value))}
      formatter={(value, name, item) => (
        <div className='flex items-center gap-2'>
          <span
            className='h-3 w-1 shrink-0 rounded-[2px]'
            style={{ background: item.color }}
          />
          <span className='font-medium text-foreground tabular-nums'>
            {format(Number(value))}
          </span>
          <span className='text-muted-foreground'>
            {config[String(name)]?.label ?? name}
          </span>
        </div>
      )}
    />
  )
}

type Column = {
  key: keyof Omit<Row, 'day'>
  label: string
  format: (v: number) => string
}

/** A chart with its table view: the same numbers, one row per day. */
function ChartCard({
  title,
  description,
  rows,
  columns,
  children,
}: {
  title: string
  description: string
  rows: Row[]
  columns: Column[]
  children: ReactNode
}) {
  const [view, setView] = useState<'chart' | 'table'>('chart')
  return (
    <Card>
      <CardHeader className='flex flex-row items-start justify-between gap-4'>
        <div className='space-y-1.5'>
          <CardTitle>{title}</CardTitle>
          <CardDescription>{description}</CardDescription>
        </div>
        <Tabs value={view} onValueChange={(v) => setView(v as typeof view)}>
          <TabsList className='h-8'>
            <TabsTrigger value='chart' className='text-xs'>
              Chart
            </TabsTrigger>
            <TabsTrigger value='table' className='text-xs'>
              Table
            </TabsTrigger>
          </TabsList>
        </Tabs>
      </CardHeader>
      <CardContent>
        {view === 'chart' ? (
          children
        ) : (
          <div className='max-h-64 overflow-y-auto'>
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Day (UTC)</TableHead>
                  {columns.map((c) => (
                    <TableHead key={c.key} className='text-right'>
                      {c.label}
                    </TableHead>
                  ))}
                </TableRow>
              </TableHeader>
              <TableBody>
                {[...rows].reverse().map((r) => (
                  <TableRow key={r.day}>
                    <TableCell>{formatDayLong(r.day)}</TableCell>
                    {columns.map((c) => (
                      <TableCell
                        key={c.key}
                        className='text-right tabular-nums'
                      >
                        {c.format(r[c.key])}
                      </TableCell>
                    ))}
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </div>
        )}
      </CardContent>
    </Card>
  )
}
