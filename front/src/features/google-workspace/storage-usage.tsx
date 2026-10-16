import { useState } from 'react'
import { Link } from '@tanstack/react-router'
import { ExternalLink } from 'lucide-react'
import {
  useWorkspaceStorageUsage,
  type WorkspaceStorageOrgUnit,
  type WorkspaceStorageUser,
} from '@/api/google-workspace'
import { formatCount } from '@/lib/format'
import { cn } from '@/lib/utils'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from '@/components/ui/card'
import { Label } from '@/components/ui/label'
import { Skeleton } from '@/components/ui/skeleton'
import { DataTable, type Column } from '@/components/data-table'
import { DirectoryError, Field } from './directory-parts'
import { DRIVE_USAGE_SCOPE } from './drive-governance'
import {
  ADMIN_STORAGE_URL,
  THRESHOLDS,
  formatPercent,
  orgUnitLabel,
  quotaText,
  storageLevel,
} from './storage-format'
import { formatMb } from './user-usage'

const PAGE_SIZE = 50

const mb = (v: bigint | undefined) => (v === undefined ? '—' : formatMb(v))

type Ids = { connectionId: string; tenantId: string }

/**
 * Every user's storage against the quota Google reports, with a summary per
 * organizational unit. It is read-only: Google offers no API to set a user
 * storage limit, so limits are changed in the Admin console.
 */
export function WorkspaceStorage({ connectionId, tenantId }: Ids) {
  const [text, setText] = useState('')
  const [query, setQuery] = useState('')
  const [orgUnitPath, setOrgUnitPath] = useState('')
  const [sort, setSort] = useState('used')
  const [threshold, setThreshold] = useState<number>(80)
  const [onlyNear, setOnlyNear] = useState(false)
  const [page, setPage] = useState(1)
  const q = useWorkspaceStorageUsage({
    connectionId,
    tenantId,
    query,
    orgUnitPath,
    sort,
    thresholdPercent: threshold,
    onlyNearLimit: onlyNear,
    page,
    pageSize: PAGE_SIZE,
  })
  const d = q.data
  const stale = q.isPlaceholderData

  const columns: Column<WorkspaceStorageUser>[] = [
    {
      header: 'Account',
      cell: (u) => (
        <div className='min-w-0'>
          <UserLink
            connectionId={connectionId}
            tenantId={tenantId}
            email={u.email}
          />
          {u.name && <p className='text-xs text-muted-foreground'>{u.name}</p>}
        </div>
      ),
    },
    { header: 'Unit', cell: (u) => orgUnitLabel(u.orgUnitPath) },
    { header: 'Gmail', cell: (u) => mb(u.gmailMb) },
    { header: 'Drive', cell: (u) => mb(u.driveMb) },
    { header: 'Photos', cell: (u) => mb(u.photosMb) },
    { header: 'Used', cell: (u) => mb(u.usedMb) },
    {
      header: 'Limit',
      cell: (u) =>
        u.quotaMb === undefined ? 'No limit reported' : mb(u.quotaMb),
    },
    {
      header: 'Usage',
      cell: (u) => <UsageMeter percent={u.usedPercent} threshold={threshold} />,
    },
  ]

  const units: Column<WorkspaceStorageOrgUnit>[] = [
    {
      header: 'Organizational unit',
      cell: (o) => (
        <Button
          variant='link'
          className='h-auto px-0'
          aria-pressed={orgUnitPath === o.path}
          onClick={() => {
            setOrgUnitPath(orgUnitPath === o.path ? '' : o.path)
            setPage(1)
          }}
        >
          {orgUnitLabel(o.path)}
        </Button>
      ),
    },
    { header: 'Users', cell: (o) => formatCount(o.userCount) },
    { header: 'Used', cell: (o) => mb(o.usedMb) },
    {
      header: 'Largest user',
      cell: (o) => (o.userCount === o.unknownCount ? '—' : mb(o.largestUsedMb)),
    },
    {
      header: 'Limit per user',
      cell: (o) => quotaText(o.quotaMb, o.quotaMixed, mb),
    },
    {
      header: 'Near / over limit',
      cell: (o) => (
        <span className='tabular-nums'>
          {formatCount(o.nearLimitCount)} / {formatCount(o.overLimitCount)}
        </span>
      ),
    },
  ]

  return (
    <div className='space-y-4'>
      <div className='flex flex-wrap items-start justify-between gap-3'>
        <div>
          <h2 className='text-xl font-semibold'>Storage</h2>
          <p className='max-w-3xl text-sm text-muted-foreground'>
            Each user’s storage against the limit Google reports for them. Neo
            Box only reads this: Google offers no API to change a user storage
            limit, so set limits per organizational unit or group in the Admin
            console.
          </p>
        </div>
        <div className='flex gap-2'>
          <Button
            variant='outline'
            disabled={q.isFetching}
            onClick={() => void q.refetch()}
          >
            Refresh
          </Button>
          <Button variant='outline' asChild>
            <a href={ADMIN_STORAGE_URL} target='_blank' rel='noreferrer'>
              Admin console <ExternalLink className='ms-1 h-3.5 w-3.5' />
            </a>
          </Button>
        </div>
      </div>

      <DirectoryError error={q.error} scope={DRIVE_USAGE_SCOPE} />
      {q.isLoading && <Skeleton className='h-64' />}
      {!q.error && d && !d.hasData && (
        <Alert>
          <AlertDescription>
            Google has no usage report for the last few days yet. Usage reports
            lag by 1–3 days; try again later. Nothing is shown rather than a
            made-up zero.
          </AlertDescription>
        </Alert>
      )}
      {!q.error && d?.hasData && (
        <div
          className={cn('space-y-4 transition-opacity', stale && 'opacity-60')}
        >
          <div className='grid gap-4 sm:grid-cols-2 xl:grid-cols-4'>
            <Stat
              label='Used in total'
              value={mb(d.totalUsedMb)}
              note={`${formatCount(d.usersRead)} users · report for ${d.date}`}
            />
            <Stat
              label={`Near limit (${d.thresholdPercent}%+)`}
              value={formatCount(d.nearLimitCount)}
              note='Under their limit but close'
              tone={d.nearLimitCount > 0n ? 'warn' : undefined}
            />
            <Stat
              label='Over limit'
              value={formatCount(d.overLimitCount)}
              note='At or over their limit'
              tone={d.overLimitCount > 0n ? 'danger' : undefined}
            />
            <Stat
              label='No limit reported'
              value={formatCount(d.noLimitCount)}
              note={
                d.unknownUsedCount > 0n
                  ? `${formatCount(d.unknownUsedCount)} more have no usage figure`
                  : 'Users Google reports no limit for'
              }
            />
          </div>
          {d.partial && (
            <Alert variant='destructive'>
              <AlertDescription>
                Incomplete: {d.partialReason || 'not every user was read'}.
                Totals cover only the users read.
              </AlertDescription>
            </Alert>
          )}

          <Card>
            <CardHeader>
              <CardTitle>By organizational unit</CardTitle>
              <CardDescription>
                Users counted in the unit they sit in directly. Select a unit to
                filter the users below.
              </CardDescription>
            </CardHeader>
            <CardContent>
              <DataTable
                columns={units}
                data={d.orgUnits}
                isLoading={false}
                emptyMessage='No organizational units'
              />
            </CardContent>
          </Card>

          <form
            className='flex flex-wrap items-end gap-3'
            onSubmit={(e) => {
              e.preventDefault()
              setQuery(text.trim())
              setPage(1)
            }}
          >
            <Field
              id='storage-search'
              label='Search users'
              value={text}
              onChange={setText}
            />
            <Select
              id='storage-sort'
              label='Sort by'
              value={sort}
              onChange={(v) => {
                setSort(v)
                setPage(1)
              }}
              options={[
                ['used', 'Most storage used'],
                ['percent', 'Fullest first'],
                ['email', 'Email'],
              ]}
            />
            <Select
              id='storage-threshold'
              label='Near limit from'
              value={String(threshold)}
              onChange={(v) => {
                setThreshold(Number(v))
                setPage(1)
              }}
              options={THRESHOLDS.map((t) => [String(t), `${t}% of limit`])}
            />
            <label className='flex items-center gap-2 pb-2 text-sm'>
              <input
                type='checkbox'
                checked={onlyNear}
                onChange={(e) => {
                  setOnlyNear(e.target.checked)
                  setPage(1)
                }}
              />
              Only near or over limit
            </label>
            <Button>Search</Button>
          </form>
          {orgUnitPath !== '' && (
            <p className='text-sm text-muted-foreground'>
              Showing users in{' '}
              <Badge variant='outline'>{orgUnitLabel(orgUnitPath)}</Badge>{' '}
              <Button
                variant='link'
                className='h-auto px-1'
                onClick={() => {
                  setOrgUnitPath('')
                  setPage(1)
                }}
              >
                Show every unit
              </Button>
            </p>
          )}
          <DataTable
            columns={columns}
            data={d.users}
            isLoading={false}
            emptyMessage='No users match'
          />
          <div className='flex items-center justify-end gap-3'>
            <span className='text-sm text-muted-foreground'>
              {formatCount(d.totalCount)} matching users · page {page}
            </span>
            <Button
              variant='outline'
              disabled={page === 1 || q.isFetching}
              onClick={() => setPage((p) => p - 1)}
            >
              Previous
            </Button>
            <Button
              variant='outline'
              disabled={
                q.isFetching || page * PAGE_SIZE >= Number(d.totalCount)
              }
              onClick={() => setPage((p) => p + 1)}
            >
              Next
            </Button>
          </div>
        </div>
      )}
    </div>
  )
}

function UserLink({ connectionId, tenantId, email }: Ids & { email: string }) {
  return (
    <Link
      to='/connections/$connectionId/tenants/$tenantId/users/$userId'
      params={{ connectionId, tenantId, userId: email }}
      search={{ tab: 'gmail' }}
      className='break-all text-primary underline-offset-4 hover:underline'
    >
      {email}
    </Link>
  )
}

/** A bar and a figure; colour is never the only signal. */
function UsageMeter({
  percent,
  threshold,
}: {
  percent: number | undefined
  threshold: number
}) {
  const level = storageLevel(percent, threshold)
  if (percent === undefined)
    return <span className='text-muted-foreground'>—</span>
  return (
    <div className='flex min-w-32 items-center gap-2'>
      <div className='h-2 w-20 overflow-hidden rounded-full bg-muted'>
        <div
          className={cn(
            'h-full rounded-full',
            level === 'over'
              ? 'bg-destructive'
              : level === 'near'
                ? 'bg-amber-500'
                : 'bg-primary'
          )}
          style={{ width: `${Math.min(100, percent)}%` }}
        />
      </div>
      <span className='tabular-nums'>{formatPercent(percent)}</span>
      {level === 'over' && <Badge variant='destructive'>Over</Badge>}
      {level === 'near' && <Badge variant='secondary'>Near</Badge>}
    </div>
  )
}

function Stat({
  label,
  value,
  note,
  tone,
}: {
  label: string
  value: string
  note: string
  tone?: 'warn' | 'danger'
}) {
  return (
    <Card className='gap-1 py-4'>
      <CardHeader className='px-4'>
        <CardDescription>{label}</CardDescription>
        <CardTitle
          className={cn(
            'text-2xl tabular-nums',
            tone === 'danger' && 'text-destructive'
          )}
        >
          {value}
        </CardTitle>
      </CardHeader>
      <CardContent className='px-4 text-xs text-muted-foreground'>
        {note}
      </CardContent>
    </Card>
  )
}

function Select({
  id,
  label,
  value,
  onChange,
  options,
}: {
  id: string
  label: string
  value: string
  onChange: (v: string) => void
  options: readonly (readonly [string, string])[]
}) {
  return (
    <div className='space-y-2'>
      <Label htmlFor={id}>{label}</Label>
      <select
        id={id}
        className='h-9 rounded-md border bg-background px-3 text-sm'
        value={value}
        onChange={(e) => onChange(e.target.value)}
      >
        {options.map(([v, text]) => (
          <option key={v} value={v}>
            {text}
          </option>
        ))}
      </select>
    </div>
  )
}
