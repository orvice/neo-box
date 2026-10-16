import { useMemo, useState, type ReactNode } from 'react'
import { Area, AreaChart, CartesianGrid, XAxis, YAxis } from 'recharts'
import { useWorkspaceUserUsage } from '@/api/google-workspace'
import { formatBytes, formatCount } from '@/lib/format'
import { cn } from '@/lib/utils'
import { Alert, AlertDescription } from '@/components/ui/alert'
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
  byteTicks,
  formatBytesTick,
  formatCompact,
  formatDayLong,
  formatDayShort,
} from '@/features/wasabi/format'
import { BLUE, NEUTRAL, ORANGE } from '@/features/wasabi/palette'
import { DirectoryError } from './directory-parts'
import { DRIVE_USAGE_SCOPE } from './drive-governance'
import {
  activitySummary,
  formatMb,
  gmailGrowth,
  storageSummary,
  usageRows,
  type StorageSplit,
  type UsageRow,
} from './user-usage'

type Ids = { connectionId: string; tenantId: string; userId: string }

const SPLIT_COLOR: Record<StorageSplit['key'], string> = {
  gmail: BLUE.light,
  drive: ORANGE.light,
  photos: NEUTRAL.light,
  other: '#d4d4d4',
}

/** Reading the usage report is shared by the page header and the Gmail tab. */
function useUsage({ connectionId, tenantId, userId }: Ids) {
  return useWorkspaceUserUsage(connectionId, tenantId, userId)
}

/**
 * How full the user's storage is: one bar split by product (Gmail, Drive,
 * Photos) and the figures beside it. Unknown stays unknown: no quota, no
 * percentage.
 */
export function StorageCard(props: Ids) {
  const q = useUsage(props)
  const s = useMemo(
    () => (q.data ? storageSummary(q.data.days) : null),
    [q.data]
  )
  return (
    <Card className='gap-3 py-4'>
      <CardHeader className='px-4'>
        <CardDescription>Storage</CardDescription>
        <CardTitle className='text-2xl tabular-nums'>
          {q.isLoading ? (
            <Skeleton className='h-8 w-28' />
          ) : s?.usedMb !== undefined ? (
            formatMb(s.usedMb)
          ) : (
            '—'
          )}
        </CardTitle>
      </CardHeader>
      <CardContent className='space-y-2 px-4'>
        {q.error ? (
          <p className='text-xs text-muted-foreground'>
            Usage report unavailable
          </p>
        ) : q.isLoading ? (
          <Skeleton className='h-2 w-full' />
        ) : !s ? (
          <p className='text-xs text-muted-foreground'>
            Google has no usage report for this user yet.
          </p>
        ) : (
          <>
            <StorageBar summary={s} />
            <p className='text-xs text-muted-foreground'>
              {s.unlimited
                ? 'No storage limit'
                : s.totalMb !== undefined && s.percent !== null
                  ? `${s.percent.toFixed(s.percent < 10 ? 1 : 0)}% of ${formatMb(s.totalMb)}`
                  : 'Quota not reported'}{' '}
              · as of {formatDayLong(s.date)}
            </p>
          </>
        )}
      </CardContent>
    </Card>
  )
}

function StorageBar({
  summary,
}: {
  summary: NonNullable<ReturnType<typeof storageSummary>>
}) {
  // The bar's full width is the quota when there is one, else the used total,
  // so a user with no cap still sees how usage divides.
  const width =
    summary.totalMb !== undefined && summary.totalMb > 0
      ? summary.totalMb
      : (summary.usedMb ?? 0)
  return (
    <div
      role='img'
      aria-label={summary.split
        .map((p) => `${p.label} ${formatMb(p.mb)}`)
        .join(', ')}
      className='flex h-2 w-full overflow-hidden rounded-full bg-muted'
    >
      {width > 0 &&
        summary.split.map((p) => (
          <div
            key={p.key}
            title={`${p.label}: ${formatMb(p.mb)}`}
            style={{
              width: `${Math.min(100, (p.mb / width) * 100)}%`,
              background: SPLIT_COLOR[p.key],
            }}
          />
        ))}
    </div>
  )
}

const storageConfig = {
  gmail: { label: 'Gmail', theme: BLUE },
  drive: { label: 'Drive', theme: ORANGE },
  photos: { label: 'Photos', theme: NEUTRAL },
} satisfies ChartConfig

const mailConfig = {
  received: { label: 'Received', theme: BLUE },
  sent: { label: 'Sent', theme: ORANGE },
} satisfies ChartConfig

/**
 * The Gmail tab's Storage & activity view: where the storage is, how it moved,
 * and how much mail came and went. All of it is from Google's daily usage
 * report, so it needs no mailbox access and lags by a few days.
 */
export function UserUsagePanel(props: Ids) {
  const q = useUsage(props)
  const days = q.data?.days
  const rows = useMemo(() => usageRows(days ?? []), [days])
  const storage = useMemo(() => storageSummary(days ?? []), [days])
  const activity = useMemo(() => activitySummary(days ?? []), [days])
  const growth = useMemo(() => gmailGrowth(days ?? []), [days])

  if (q.isLoading) return <Skeleton className='h-64' />
  if (q.error)
    return <DirectoryError error={q.error} scope={DRIVE_USAGE_SCOPE} />
  if (!days?.length)
    return (
      <Alert>
        <AlertDescription>
          Google has no usage report for this user in the last{' '}
          {q.data?.daysRequested} days. Reports lag by a few days and only cover
          accounts with activity.
        </AlertDescription>
      </Alert>
    )

  const gmailMb = [...days]
    .reverse()
    .find((d) => d.gmailUsedMb !== undefined)?.gmailUsedMb
  const storageTicks = byteTicks(
    Math.max(
      0,
      ...rows.map((r) => (r.gmail ?? 0) + (r.drive ?? 0) + (r.photos ?? 0))
    )
  )
  const countMax = Math.max(
    0,
    ...rows.map((r) => (r.received ?? 0) + (r.sent ?? 0))
  )
  const hasSplit = rows.some((r) => r.gmail !== undefined)
  const hasMail = rows.some(
    (r) => r.received !== undefined || r.sent !== undefined
  )

  return (
    <div className='space-y-4'>
      <div className='grid gap-4 sm:grid-cols-2 xl:grid-cols-4'>
        <Stat
          label='Gmail storage'
          value={gmailMb === undefined ? '—' : formatMb(gmailMb)}
          note={
            storage?.usedMb !== undefined &&
            gmailMb !== undefined &&
            storage.usedMb > 0
              ? `${Math.round((Number(gmailMb) / storage.usedMb) * 100)}% of everything this user stores`
              : 'Share of total storage not reported'
          }
        />
        <Stat
          label='Gmail growth'
          value={
            growth
              ? `${growth.deltaMb >= 0 ? '+' : '−'}${formatMb(Math.abs(growth.deltaMb))}`
              : '—'
          }
          note={
            growth
              ? `${formatDayShort(growth.fromDate)} to ${formatDayShort(growth.toDate)}, about ${formatMb(Math.abs(Math.round(growth.perDayMb)))} a day`
              : 'Needs two days with a Gmail figure'
          }
        />
        <Stat
          label='Received'
          value={activity ? formatCount(activity.received) : '—'}
          note={
            activity
              ? `${formatCount(activity.receivedPerDay)} a day · ${formatCount(activity.spam)} spam`
              : 'No message counts reported'
          }
        />
        <Stat
          label='Sent'
          value={activity ? formatCount(activity.sent) : '—'}
          note={
            activity
              ? `${formatCount(activity.sentPerDay)} a day${activity.busiest ? ` · busiest ${formatDayShort(activity.busiest.date)}` : ''}`
              : 'No message counts reported'
          }
        />
      </div>

      {hasSplit && (
        <ChartCard
          title='Storage over time'
          description='Storage by product in the daily report, in binary units. Gaps are days Google reported nothing.'
          rows={rows}
          columns={[
            { key: 'gmail', label: 'Gmail', format: bytesOrDash },
            { key: 'drive', label: 'Drive', format: bytesOrDash },
            { key: 'photos', label: 'Photos', format: bytesOrDash },
          ]}
        >
          <ChartContainer config={storageConfig} className='h-60 w-full'>
            <AreaChart data={rows} margin={{ top: 8, right: 12, left: 4 }}>
              <CartesianGrid vertical={false} />
              {dayAxis}
              <YAxis
                {...yAxisProps}
                ticks={storageTicks}
                domain={[0, storageTicks[storageTicks.length - 1]]}
                tickFormatter={formatBytesTick}
              />
              <ChartTooltip
                content={
                  <Tooltip config={storageConfig} format={bytesOrDash} />
                }
              />
              <Area {...areaProps('gmail')} stackId='storage' />
              <Area {...areaProps('drive')} stackId='storage' />
              <Area {...areaProps('photos')} stackId='storage' />
              <ChartLegend content={<ChartLegendContent />} />
            </AreaChart>
          </ChartContainer>
        </ChartCard>
      )}

      {hasMail && (
        <ChartCard
          title='Mail per day'
          description='Messages received and sent each day. A day Google did not report is left blank, not shown as zero.'
          rows={rows}
          columns={[
            { key: 'received', label: 'Received', format: countOrDash },
            { key: 'sent', label: 'Sent', format: countOrDash },
          ]}
        >
          <ChartContainer config={mailConfig} className='h-52 w-full'>
            <AreaChart data={rows} margin={{ top: 8, right: 12, left: 4 }}>
              <CartesianGrid vertical={false} />
              {dayAxis}
              <YAxis
                {...yAxisProps}
                domain={[0, Math.max(countMax, 1)]}
                allowDecimals={false}
                tickFormatter={(v) => formatCompact(v)}
              />
              <ChartTooltip
                content={<Tooltip config={mailConfig} format={countOrDash} />}
              />
              <Area {...areaProps('received')} />
              <Area {...areaProps('sent')} />
              <ChartLegend content={<ChartLegendContent />} />
            </AreaChart>
          </ChartContainer>
        </ChartCard>
      )}

      <p className='text-xs text-muted-foreground'>
        From Google’s daily usage reports for the last {q.data?.daysRequested}{' '}
        days. The newest day is usually two or three days old. Neo Box reads no
        mail for this view, so it is not recorded under Neo Box operations.
      </p>
    </div>
  )
}

const bytesOrDash = (v: number | undefined) =>
  v === undefined ? '—' : formatBytes(v)
const countOrDash = (v: number | undefined) =>
  v === undefined ? '—' : formatCount(v)

function Stat({
  label,
  value,
  note,
}: {
  label: string
  value: string
  note: string
}) {
  return (
    <Card className='gap-1 py-4'>
      <CardHeader className='px-4'>
        <CardDescription>{label}</CardDescription>
        <CardTitle className='text-2xl tabular-nums'>{value}</CardTitle>
      </CardHeader>
      <CardContent className='px-4 text-xs text-muted-foreground'>
        {note}
      </CardContent>
    </Card>
  )
}

// --- chart parts (the same conventions as the Wasabi usage charts) ---

const dayAxis = (
  <XAxis
    dataKey='day'
    tickLine={false}
    axisLine={false}
    tickMargin={8}
    minTickGap={32}
    tickFormatter={formatDayShort}
  />
)

const yAxisProps = {
  tickLine: false,
  axisLine: false,
  width: 64,
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

function Tooltip(
  props: React.ComponentProps<typeof ChartTooltipContent> & {
    config: ChartConfig
    format: (v: number | undefined) => string
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
            {format(value === undefined ? undefined : Number(value))}
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
  key: keyof Omit<UsageRow, 'day'>
  label: string
  format: (v: number | undefined) => string
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
  rows: UsageRow[]
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
      <CardContent
        className={cn(view === 'table' && 'max-h-72 overflow-y-auto')}
      >
        {view === 'chart' ? (
          children
        ) : (
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Day</TableHead>
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
                    <TableCell key={c.key} className='text-right tabular-nums'>
                      {c.format(r[c.key])}
                    </TableCell>
                  ))}
                </TableRow>
              ))}
            </TableBody>
          </Table>
        )}
      </CardContent>
    </Card>
  )
}
