import { useState } from 'react'
import {
  useWorkspaceDriveActivities,
  useWorkspaceDriveUsage,
  type WorkspaceDriveActivity,
  type WorkspaceDriveEvent,
  type WorkspaceDriveUsageUser,
} from '@/api/google-workspace'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { DataTable, type Column } from '@/components/data-table'
import { CursorPager, DirectoryError, Field } from './directory-parts'
import { useCursor } from './directory-state'
import {
  DRIVE_AUDIT_SCOPE,
  DRIVE_PRESETS,
  DRIVE_USAGE_SCOPE,
  driveEventSummary,
  exposureOf,
  formatQuota,
  formatShare,
  formatUsageMB,
  humanizeDriveValue,
  usageShareTone,
  type ExposureTone,
} from './drive-governance'
import { useGmailConfirmation } from './gmail-access'
import { formatWorkspaceTime } from './observability-format'
import { WorkspaceSharedDrives } from './shared-drives'

type Ids = { connectionId: string; tenantId: string }

function toneVariant(tone: ExposureTone) {
  return tone === 'danger'
    ? 'destructive'
    : tone === 'warn'
      ? 'secondary'
      : 'outline'
}

function localTime(date: Date) {
  const local = new Date(date.getTime() - date.getTimezoneOffset() * 60000)
  return local.toISOString().slice(0, 16)
}

export function WorkspaceDrive(props: Ids) {
  const drives = useGmailConfirmation()
  return (
    <section className='space-y-4'>
      <h2 className='text-xl font-semibold'>Drive governance</h2>
      <p className='text-sm text-muted-foreground'>
        Sharing activity and storage usage come live from Google's Reports API:
        who shared, moved or deleted a file and how much storage accounts use,
        never file content. The shared drive list uses a Drive scope, so it asks
        for a reason first and is recorded. Nothing here changes sharing.
      </p>
      <Tabs defaultValue='activity'>
        <TabsList>
          <TabsTrigger value='activity'>Sharing activity</TabsTrigger>
          <TabsTrigger value='usage'>Storage usage</TabsTrigger>
          <TabsTrigger value='shared-drives'>Shared drives</TabsTrigger>
        </TabsList>
        <TabsContent value='activity' className='pt-4'>
          <DriveActivity {...props} />
        </TabsContent>
        <TabsContent value='usage' className='pt-4'>
          <DriveUsage {...props} />
        </TabsContent>
        <TabsContent value='shared-drives' className='pt-4'>
          <WorkspaceSharedDrives
            {...props}
            confirmation={drives.confirmation}
            onConfirmation={drives.setConfirmation}
          />
        </TabsContent>
      </Tabs>
    </section>
  )
}

function DriveActivity({ connectionId, tenantId }: Ids) {
  const [preset, setPreset] = useState('public_web')
  const [userKey, setUserKey] = useState('')
  const [side, setSide] = useState(false)
  const [start, setStart] = useState(() =>
    localTime(new Date(Date.now() - 7 * 86400000))
  )
  const [end, setEnd] = useState(() => localTime(new Date()))
  const [error, setError] = useState('')
  const [selected, setSelected] = useState<WorkspaceDriveActivity | null>(null)
  const cursor = useCursor()
  const [filters, setFilters] = useState(() => ({
    preset: 'public_web',
    userKey: '',
    includeSideEffects: false,
    startTime: new Date(Date.now() - 7 * 86400000).toISOString(),
    endTime: new Date().toISOString(),
  }))
  const q = useWorkspaceDriveActivities({
    connectionId,
    tenantId,
    ...filters,
    pageToken: cursor.token,
    pageSize: 50,
  })
  const columns: Column<WorkspaceDriveActivity>[] = [
    { header: 'Time', cell: (a) => formatWorkspaceTime(a.occurredAt) },
    {
      header: 'Actor',
      cell: (a) => a.actorEmail || a.actorProfileId || 'Not provided',
    },
    {
      header: 'File',
      cell: (a) => (
        <div className='max-w-64'>
          <div className='truncate'>
            {a.events[0]?.docTitle || 'Not provided'}
          </div>
          <div className='truncate text-xs text-muted-foreground'>
            {a.events[0]?.owner
              ? `Owner ${a.events[0].owner}`
              : 'Owner not provided'}
          </div>
        </div>
      ),
    },
    {
      header: 'What happened',
      cell: (a) => (
        <Button
          variant='link'
          className='px-0 text-start whitespace-normal'
          onClick={() => setSelected(a)}
        >
          {a.events.map((e) => driveEventSummary(e)).join('; ')}
        </Button>
      ),
    },
    {
      header: 'Exposure',
      cell: (a) => {
        const e = exposureOf(a.events[0]?.visibility ?? '')
        return <Badge variant={toneVariant(e.tone)}>{e.label}</Badge>
      },
    },
  ]
  return (
    <div className='space-y-4'>
      <form
        className='space-y-3'
        onSubmit={(e) => {
          e.preventDefault()
          setError('')
          const a = new Date(start)
          const b = new Date(end)
          if (
            !Number.isFinite(a.getTime()) ||
            !Number.isFinite(b.getTime()) ||
            a >= b
          ) {
            setError('Choose a valid start before end')
            return
          }
          setFilters({
            preset,
            userKey: userKey.trim(),
            includeSideEffects: side,
            startTime: a.toISOString(),
            endTime: b.toISOString(),
          })
          cursor.reset()
        }}
      >
        <div className='grid gap-3 sm:grid-cols-3'>
          <div className='space-y-2'>
            <Label htmlFor='drive-preset'>Activity</Label>
            <select
              id='drive-preset'
              className='h-9 w-full rounded-md border bg-background px-3 text-sm'
              value={preset}
              onChange={(e) => setPreset(e.target.value)}
            >
              {DRIVE_PRESETS.map((p) => (
                <option key={p.key} value={p.key}>
                  {p.label}
                </option>
              ))}
            </select>
          </div>
          <Field
            id='drive-user'
            label='User email or ID (optional)'
            value={userKey}
            onChange={setUserKey}
          />
          <div className='flex items-end gap-2 pb-2'>
            <input
              id='drive-side'
              type='checkbox'
              checked={side}
              onChange={(e) => setSide(e.target.checked)}
            />
            <Label htmlFor='drive-side'>Include side effects</Label>
          </div>
        </div>
        <div className='grid gap-3 sm:grid-cols-3'>
          <div className='space-y-2'>
            <Label htmlFor='drive-start'>Start (local time)</Label>
            <Input
              id='drive-start'
              type='datetime-local'
              value={start}
              onChange={(e) => setStart(e.target.value)}
              required
            />
          </div>
          <div className='space-y-2'>
            <Label htmlFor='drive-end'>End (local time)</Label>
            <Input
              id='drive-end'
              type='datetime-local'
              value={end}
              onChange={(e) => setEnd(e.target.value)}
              required
            />
          </div>
          <div className='flex items-end gap-2'>
            <Button>Apply filters</Button>
            <Button
              type='button'
              variant='outline'
              disabled={q.isFetching}
              onClick={() => void q.refetch()}
            >
              Refresh
            </Button>
          </div>
        </div>
      </form>
      <p className='text-xs text-muted-foreground'>
        Drive audit events can arrive minutes to hours late and are kept for 180
        days. Linked changes such as inherited folder access are hidden unless
        you include side effects.
      </p>
      <DirectoryError error={error || q.error} scope={DRIVE_AUDIT_SCOPE} />
      {!q.error && !!q.data?.hiddenEventCount && (
        <Alert>
          <AlertDescription>
            {String(q.data.hiddenEventCount)} side-effect event(s) on this page
            are hidden.
          </AlertDescription>
        </Alert>
      )}
      {!q.error && (
        <DataTable
          columns={columns}
          data={q.data?.activities}
          isLoading={q.isLoading}
          emptyMessage='No Drive activity matches in this time range'
        />
      )}
      <CursorPager
        cursor={cursor}
        nextToken={q.error ? undefined : q.data?.nextPageToken}
        busy={q.isFetching}
      />
      {selected && (
        <Dialog
          open
          onOpenChange={(open) => {
            if (!open) setSelected(null)
          }}
        >
          <DialogContent className='max-h-[85vh] overflow-y-auto sm:max-w-3xl'>
            <DialogHeader>
              <DialogTitle>Drive activity details</DialogTitle>
            </DialogHeader>
            <p className='text-sm'>
              {formatWorkspaceTime(selected.occurredAt)} ·{' '}
              {selected.actorEmail || selected.actorProfileId || 'Not provided'}{' '}
              · {selected.ipAddress || 'IP not provided'}
            </p>
            {selected.events.map((event, i) => (
              <DriveEventDetail key={i} event={event} />
            ))}
          </DialogContent>
        </Dialog>
      )}
    </div>
  )
}

function DriveEventDetail({ event }: { event: WorkspaceDriveEvent }) {
  const rows: [string, string][] = [
    ['File', event.docTitle],
    ['Type', event.docType],
    ['Owner', event.owner],
    ['File ID', event.docId],
    ['Shared drive', event.sharedDriveId],
    ['Visibility', event.visibility && humanizeDriveValue(event.visibility)],
    [
      'Previous visibility',
      event.oldVisibility && humanizeDriveValue(event.oldVisibility),
    ],
    ['Target', event.targetUser || event.targetDomain || event.target],
  ]
  return (
    <div className='space-y-2'>
      <h3 className='font-semibold'>{driveEventSummary(event)}</h3>
      <p className='text-xs text-muted-foreground'>
        {event.name}
        {event.primary === false && ' · side effect of another change'}
      </p>
      <dl className='grid grid-cols-[max-content_1fr] gap-x-4 gap-y-1 text-sm'>
        {rows.map(([label, value]) => (
          <div key={label} className='contents'>
            <dt className='text-muted-foreground'>{label}</dt>
            <dd className='break-all'>{value || 'Not provided'}</dd>
          </div>
        ))}
      </dl>
      <pre className='overflow-auto rounded-md bg-muted p-3 text-xs'>
        {JSON.stringify(event.parameters ?? {}, null, 2)}
      </pre>
    </div>
  )
}

function DriveUsage({ connectionId, tenantId }: Ids) {
  const [text, setText] = useState('')
  const [query, setQuery] = useState('')
  const [sort, setSort] = useState('used')
  const [page, setPage] = useState(1)
  const q = useWorkspaceDriveUsage({
    connectionId,
    tenantId,
    query,
    sort,
    page,
    pageSize: 50,
  })
  const d = q.data
  const columns: Column<WorkspaceDriveUsageUser>[] = [
    { header: 'Account', accessorKey: 'email' },
    { header: 'Drive used', cell: (u) => formatUsageMB(u.usedMb) },
    { header: 'Quota', cell: (u) => formatQuota(u.totalMb) },
    {
      header: 'Share of quota',
      cell: (u) => (
        <Badge variant={toneVariant(usageShareTone(u.sharePercent))}>
          {formatShare(u.sharePercent)}
        </Badge>
      ),
    },
  ]
  return (
    <div className='space-y-4'>
      <form
        className='flex flex-wrap items-end gap-3'
        onSubmit={(e) => {
          e.preventDefault()
          setQuery(text.trim())
          setPage(1)
        }}
      >
        <Field
          id='drive-usage-search'
          label='Search accounts'
          value={text}
          onChange={setText}
        />
        <div className='space-y-2'>
          <Label htmlFor='drive-usage-sort'>Sort by</Label>
          <select
            id='drive-usage-sort'
            className='h-9 rounded-md border bg-background px-3 text-sm'
            value={sort}
            onChange={(e) => {
              setSort(e.target.value)
              setPage(1)
            }}
          >
            <option value='used'>Most storage used</option>
            <option value='email'>Email</option>
          </select>
        </div>
        <Button>Search</Button>
        <Button
          type='button'
          variant='outline'
          disabled={q.isFetching}
          onClick={() => void q.refetch()}
        >
          Refresh
        </Button>
      </form>
      <DirectoryError error={q.error} scope={DRIVE_USAGE_SCOPE} />
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
        <>
          <p className='text-sm text-muted-foreground'>
            Report for {d.date} (UTC) · {String(d.usersRead)} accounts read ·{' '}
            {formatUsageMB(d.totalUsedMb)} used in total
            {d.unknownUsedCount > 0n &&
              ` · ${String(d.unknownUsedCount)} account(s) without a figure`}
          </p>
          {d.partial && (
            <Alert variant='destructive'>
              <AlertDescription>
                Incomplete: {d.partialReason || 'not every account was read'}.
                Totals cover only the accounts read.
              </AlertDescription>
            </Alert>
          )}
          <DataTable
            columns={columns}
            data={d.users}
            isLoading={q.isLoading}
            emptyMessage='No accounts match this search'
          />
          <div className='flex items-center justify-end gap-3'>
            <span className='text-sm text-muted-foreground'>
              {String(d.totalCount)} matching accounts · page {page}
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
              disabled={q.isFetching || page * 50 >= Number(d.totalCount)}
              onClick={() => setPage((p) => p + 1)}
            >
              Next
            </Button>
          </div>
        </>
      )}
      {!d && q.isLoading && (
        <p className='text-sm text-muted-foreground'>Reading usage…</p>
      )}
    </div>
  )
}
