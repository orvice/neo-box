import { useState } from 'react'
import {
  useWorkspaceAudit,
  type WorkspaceAuditActivity,
} from '@/api/google-workspace'
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { DataTable, type Column } from '@/components/data-table'
import { CursorPager, DirectoryError, Field } from './directory-parts'
import { useCursor } from './directory-state'
import { formatWorkspaceTime } from './observability-format'

const AUDIT_SCOPE =
  'https://www.googleapis.com/auth/admin.reports.audit.readonly'
function localTime(date: Date) {
  const local = new Date(date.getTime() - date.getTimezoneOffset() * 60000)
  return local.toISOString().slice(0, 16)
}
export function WorkspaceAudit({
  connectionId,
  tenantId,
}: {
  connectionId: string
  tenantId: string
}) {
  const [application, setApplication] = useState('login')
  const [userKey, setUserKey] = useState('')
  const [eventName, setEventName] = useState('')
  const [start, setStart] = useState(() =>
    localTime(new Date(Date.now() - 7 * 86400000))
  )
  const [end, setEnd] = useState(() => localTime(new Date()))
  const [error, setError] = useState('')
  const [selected, setSelected] = useState<WorkspaceAuditActivity | null>(null)
  const cursor = useCursor()
  const [filters, setFilters] = useState(() => ({
    application: 'login',
    userKey: '',
    eventName: '',
    startTime: new Date(Date.now() - 7 * 86400000).toISOString(),
    endTime: new Date().toISOString(),
  }))
  const audit = useWorkspaceAudit({
    connectionId,
    tenantId,
    ...filters,
    pageToken: cursor.token,
    pageSize: 50,
  })
  const columns: Column<WorkspaceAuditActivity>[] = [
    { header: 'Time', cell: (a) => formatWorkspaceTime(a.occurredAt) },
    { header: 'Application', accessorKey: 'application' },
    {
      header: 'Actor',
      cell: (a) => a.actorEmail || a.actorProfileId || 'Not provided',
    },
    { header: 'IP address', cell: (a) => a.ipAddress || 'Not provided' },
    {
      header: 'Events',
      cell: (a) => (
        <Button
          variant='link'
          className='px-0 text-start whitespace-normal'
          onClick={() => setSelected(a)}
        >
          {a.events.map((e) => e.name).join(', ') || 'Details'}
        </Button>
      ),
    },
  ]
  return (
    <section className='space-y-4'>
      <div className='flex items-center justify-between gap-3'>
        <h2 className='text-xl font-semibold'>Google audit activities</h2>
        <Button
          variant='outline'
          disabled={audit.isFetching}
          onClick={() => void audit.refetch()}
        >
          Refresh
        </Button>
      </div>
      <p className='text-sm text-muted-foreground'>
        Read live from Google's Reports API for this tenant. Event availability
        and delivery delays depend on Google, the tenant's edition and
        administrator privileges. This is separate from the Neo Box operation
        log.
      </p>
      <form
        className='space-y-3'
        onSubmit={(e) => {
          e.preventDefault()
          setError('')
          const a = new Date(start),
            b = new Date(end)
          if (
            !Number.isFinite(a.getTime()) ||
            !Number.isFinite(b.getTime()) ||
            a >= b
          ) {
            setError('Choose a valid start before end')
            return
          }
          setFilters({
            application,
            userKey: userKey.trim(),
            eventName: eventName.trim(),
            startTime: a.toISOString(),
            endTime: b.toISOString(),
          })
          cursor.reset()
        }}
      >
        <div className='grid gap-3 sm:grid-cols-3'>
          <div className='space-y-2'>
            <Label htmlFor='audit-application'>Application</Label>
            <select
              id='audit-application'
              className='h-9 w-full rounded-md border bg-background px-3 text-sm'
              value={application}
              onChange={(e) => setApplication(e.target.value)}
            >
              {[
                'login',
                'admin',
                'groups',
                'groups_enterprise',
                'token',
                'user_accounts',
              ].map((v) => (
                <option key={v} value={v}>
                  {v.replace(/_/g, ' ')}
                </option>
              ))}
            </select>
          </div>
          <Field
            id='audit-user'
            label='User email or ID (optional)'
            value={userKey}
            onChange={setUserKey}
          />
          <Field
            id='audit-event'
            label='Event name (optional)'
            value={eventName}
            onChange={setEventName}
          />
        </div>
        <div className='grid gap-3 sm:grid-cols-3'>
          <div className='space-y-2'>
            <Label htmlFor='audit-start'>Start (local time)</Label>
            <Input
              id='audit-start'
              type='datetime-local'
              value={start}
              onChange={(e) => setStart(e.target.value)}
              required
            />
          </div>
          <div className='space-y-2'>
            <Label htmlFor='audit-end'>End (local time)</Label>
            <Input
              id='audit-end'
              type='datetime-local'
              value={end}
              onChange={(e) => setEnd(e.target.value)}
              required
            />
          </div>
          <div className='flex items-end'>
            <Button>Apply filters</Button>
          </div>
        </div>
      </form>
      <DirectoryError error={error || audit.error} scope={AUDIT_SCOPE} />
      {!audit.error && (
        <DataTable
          columns={columns}
          data={audit.data?.activities}
          isLoading={audit.isLoading}
          emptyMessage='No Google activities in this time range'
        />
      )}
      <CursorPager
        cursor={cursor}
        nextToken={audit.error ? undefined : audit.data?.nextPageToken}
        busy={audit.isFetching}
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
              <DialogTitle>Google activity details</DialogTitle>
            </DialogHeader>
            <p className='text-sm'>
              {formatWorkspaceTime(selected.occurredAt)} ·{' '}
              {selected.actorEmail || selected.actorProfileId || 'Not provided'}
            </p>
            {selected.events.map((event, index) => (
              <div key={index} className='space-y-2'>
                <h3 className='font-semibold'>{event.name}</h3>
                <p className='text-xs text-muted-foreground'>{event.type}</p>
                <pre className='overflow-auto rounded-md bg-muted p-3 text-xs'>
                  {JSON.stringify(
                    event.parameters ? event.parameters : {},
                    null,
                    2
                  )}
                </pre>
              </div>
            ))}
          </DialogContent>
        </Dialog>
      )}
    </section>
  )
}
