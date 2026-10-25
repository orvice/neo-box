import { useMemo, useState } from 'react'
import {
  useCloudflareEmailLogs,
  useCloudflareEmailRouting,
  type CloudflareEmailLog,
} from '@/api/cloudflare'
import { cn } from '@/lib/utils'
import { Badge } from '@/components/ui/badge'
import { Input } from '@/components/ui/input'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { DataTable, type Column } from '@/components/data-table'
import {
  DEFAULT_LOG_LIMIT,
  DEFAULT_LOG_RANGE_MS,
  LOGS_PERMISSION,
  LOG_LIMITS,
  LOG_RANGES,
  authFailed,
  authSummary,
  emptyLogsMessage,
  eventTypeLabel,
  filterLogs,
  logStatuses,
  ruleLabel,
  statusClass,
  statusLabel,
} from './email-logs'
import { ProblemAlert } from './list-parts'
import { formatUTC } from './operation-status'

const ANY = 'any'

/**
 * A Zone's Email Routing logs: the mail Cloudflare received for it and what
 * it did with each (forwarded, dropped, rejected, failed), newest first.
 * They are read live from Cloudflare's GraphQL Analytics API (kept there 31
 * days) and are not stored in Neo Box.
 */
export function EmailLogsTab({
  connectionId,
  zoneId,
}: {
  connectionId: string
  zoneId: string
}) {
  const [rangeMs, setRangeMs] = useState(DEFAULT_LOG_RANGE_MS)
  const [limit, setLimit] = useState(DEFAULT_LOG_LIMIT)
  const [status, setStatus] = useState('')
  const [search, setSearch] = useState('')
  const logs = useCloudflareEmailLogs(connectionId, zoneId, rangeMs, limit)
  // Only names the matched rule; without the permission it shows IDs.
  const routing = useCloudflareEmailRouting(connectionId, zoneId)

  const all = useMemo(() => logs.data?.logs ?? [], [logs.data])
  const statuses = useMemo(() => logStatuses(all), [all])
  const shown = useMemo(
    () => filterLogs(all, { status, search }),
    [all, status, search]
  )

  const columns: Column<CloudflareEmailLog>[] = [
    {
      header: 'Time',
      cell: (l) => (
        <span className='font-mono text-xs whitespace-nowrap'>
          {formatUTC(l.time)}
        </span>
      ),
    },
    {
      header: 'Mail',
      cell: (l) => (
        <div className='max-w-sm space-y-0.5'>
          <div className='truncate font-mono text-xs' title={l.from}>
            {l.from || '-'}
          </div>
          <div
            className='truncate font-mono text-xs text-muted-foreground'
            title={l.to}
          >
            → {l.to || '-'}
          </div>
          {l.subject && (
            <div className='truncate text-sm' title={l.subject}>
              {l.subject}
            </div>
          )}
        </div>
      ),
    },
    {
      header: 'Result',
      cell: (l) => (
        <div className='max-w-xs space-y-1'>
          <div className='flex flex-wrap items-center gap-1'>
            <Badge className={cn(statusClass(l.status))}>
              {statusLabel(l.status)}
            </Badge>
            {l.spam && <Badge variant='secondary'>Spam</Badge>}
            {l.ndr && <Badge variant='secondary'>Bounce</Badge>}
          </div>
          <div className='text-xs text-muted-foreground'>
            {eventTypeLabel(l.eventType)}
            {l.action && ` · ${l.action}`}
          </div>
          {l.errorDetail && (
            <div className='text-xs break-words text-danger-foreground'>
              {l.errorDetail}
            </div>
          )}
        </div>
      ),
    },
    {
      header: 'Rule',
      cell: (l) => (
        <span
          className='font-mono text-xs break-all'
          title={l.ruleId || undefined}
        >
          {ruleLabel(l.ruleId, routing.data?.rules)}
        </span>
      ),
    },
    {
      header: 'Authentication',
      cell: (l) => (
        <span
          className={cn(
            'text-xs whitespace-nowrap',
            authFailed(l) ? 'text-danger-foreground' : 'text-muted-foreground'
          )}
          title={l.messageId ? `Message ID ${l.messageId}` : undefined}
        >
          {authSummary(l)}
        </span>
      ),
    },
  ]

  return (
    <div className='space-y-4'>
      <div className='flex flex-col gap-2 sm:flex-row sm:flex-wrap sm:items-center'>
        <Input
          value={search}
          onChange={(e) => setSearch(e.target.value)}
          placeholder='Search sender, recipient, subject…'
          className='sm:max-w-xs'
          aria-label='Search the logs'
          autoComplete='off'
        />
        <Select
          value={String(rangeMs)}
          onValueChange={(v) => setRangeMs(Number(v))}
        >
          <SelectTrigger className='w-full sm:w-44' aria-label='Time range'>
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            {LOG_RANGES.map((r) => (
              <SelectItem key={r.ms} value={String(r.ms)}>
                {r.label}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
        <Select
          value={status || ANY}
          onValueChange={(v) => setStatus(v === ANY ? '' : v)}
        >
          <SelectTrigger className='w-full sm:w-44' aria-label='Result'>
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value={ANY}>Any result</SelectItem>
            {statuses.map((s) => (
              <SelectItem key={s} value={s}>
                {statusLabel(s)}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
        <Select
          value={String(limit)}
          onValueChange={(v) => setLimit(Number(v))}
        >
          <SelectTrigger className='w-full sm:w-36' aria-label='Event limit'>
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            {LOG_LIMITS.map((n) => (
              <SelectItem key={n} value={String(n)}>
                Newest {n}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
      </div>
      {logs.error ? (
        <ProblemAlert error={logs.error} permission={LOGS_PERMISSION} />
      ) : (
        <>
          <DataTable
            columns={columns}
            data={shown}
            isLoading={logs.isLoading}
            emptyMessage={emptyLogsMessage(all.length, { status, search })}
            emptyDescription='Cloudflare lists an event when it receives or handles mail for this zone.'
          />
          <p className='text-xs text-muted-foreground'>
            {logs.data?.truncated
              ? `Showing the newest ${all.length} events; older ones in this range exist; narrow the range to see them. `
              : ''}
            Read live from Cloudflare, which keeps these logs for 31 days. One
            email can have several events (received, then forwarded).
          </p>
        </>
      )}
    </div>
  )
}
