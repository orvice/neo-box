import { useState, type FormEvent, type ReactNode } from 'react'
import { timestampDate } from '@bufbuild/protobuf/wkt'
import { toast } from 'sonner'
import {
  useWorkspaceGmailSettings,
  useWorkspaceGmailVacation,
  type GmailSectionState,
  type GmailSettings,
  type GmailVacation,
} from '@/api/google-workspace'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Switch } from '@/components/ui/switch'
import { Textarea } from '@/components/ui/textarea'
import { DataTable, type Column } from '@/components/data-table'
import { DirectoryError, Field } from './directory-parts'
import { GMAIL_SCOPE } from './directory-state'
import {
  exposure,
  filterCriteria,
  filterRisks,
  fromLocalInput,
  toLocalInput,
  vacationError,
  type GmailConfirmation,
} from './gmail-access'
import { formatWorkspaceTime } from './observability-format'

export type Props = {
  connectionId: string
  tenantId: string
  userId: string
  userEmail: string
  confirmation: GmailConfirmation | null
  onConfirmation: (c: GmailConfirmation | null) => void
}

export function SettingsView(
  props: Props & { confirmation: GmailConfirmation }
) {
  const { connectionId, tenantId, userId, confirmation } = props
  const access = {
    reason: confirmation.reason,
    targetEmail: confirmation.targetEmail,
  }
  const q = useWorkspaceGmailSettings(
    connectionId,
    tenantId,
    userId,
    access,
    confirmation.confirmedAt
  )
  return (
    <div className='space-y-4'>
      <div className='flex justify-end'>
        <Button
          variant='outline'
          size='sm'
          disabled={q.isFetching}
          onClick={() => q.refetch()}
          title='Reads the settings again; each read is recorded'
        >
          {q.isFetching ? 'Reading…' : 'Read again'}
        </Button>
      </div>
      <DirectoryError error={q.error} scope={GMAIL_SCOPE} />
      {q.isLoading && <p>Reading settings…</p>}
      {q.data && (
        <Settings
          key={String(q.data.readAt?.seconds ?? 0)}
          data={q.data}
          {...props}
          access={access}
        />
      )}
    </div>
  )
}

function Section({
  title,
  state,
  children,
}: {
  title: string
  state: GmailSectionState | undefined
  children: ReactNode
}) {
  return (
    <section className='space-y-2 rounded-md border p-4'>
      <h3 className='font-semibold'>{title}</h3>
      {state && !state.ok ? (
        <p className='text-sm text-muted-foreground'>
          Not read: {state.error || 'unknown reason'}
        </p>
      ) : (
        children
      )}
    </section>
  )
}

function ExposureBadge({ external }: { external: boolean | undefined }) {
  const e = exposure(external)
  if (e === 'external')
    return <Badge variant='destructive'>Outside the tenant</Badge>
  if (e === 'internal') return <Badge variant='outline'>In the tenant</Badge>
  return <Badge variant='secondary'>Not checked</Badge>
}

function Settings(
  props: Props & {
    data: GmailSettings
    access: { reason: string; targetEmail: string }
  }
) {
  const { data } = props
  return (
    <div className='space-y-4'>
      <p className='text-xs text-muted-foreground'>
        Read {formatWorkspaceTime(data.readAt)}. Values not shown could not be
        read; they are not “off”.
      </p>
      <Section title='Auto-reply' state={data.vacation?.state}>
        {data.vacation && (
          <VacationEditor
            vacation={data.vacation}
            connectionId={props.connectionId}
            tenantId={props.tenantId}
            userId={props.userId}
            access={props.access}
          />
        )}
      </Section>
      <Section title='Automatic forwarding' state={data.autoForwarding?.state}>
        {data.autoForwarding?.enabled ? (
          <div className='flex flex-wrap items-center gap-2 text-sm'>
            <span>
              On, to{' '}
              <span className='break-all'>
                {data.autoForwarding.emailAddress}
              </span>
            </span>
            <ExposureBadge external={data.autoForwarding.external} />
            <span className='text-muted-foreground'>
              ({data.autoForwarding.disposition || 'default handling'})
            </span>
          </div>
        ) : (
          <p className='text-sm'>Off</p>
        )}
      </Section>
      <Section
        title='Forwarding addresses'
        state={data.forwardingAddresses?.state}
      >
        <DataTable
          columns={
            [
              { header: 'Address', accessorKey: 'email' },
              { header: 'Status', accessorKey: 'verificationStatus' },
              {
                header: 'Destination',
                cell: (a) => <ExposureBadge external={a.external} />,
              },
            ] as Column<
              NonNullable<
                GmailSettings['forwardingAddresses']
              >['addresses'][number]
            >[]
          }
          data={data.forwardingAddresses?.addresses}
          isLoading={false}
          emptyMessage='No forwarding addresses'
        />
      </Section>
      <Section title='Delegates' state={data.delegates?.state}>
        <DataTable
          columns={
            [
              { header: 'Delegate', accessorKey: 'email' },
              { header: 'Status', accessorKey: 'verificationStatus' },
            ] as Column<
              NonNullable<GmailSettings['delegates']>['delegates'][number]
            >[]
          }
          data={data.delegates?.delegates}
          isLoading={false}
          emptyMessage='No delegates'
        />
      </Section>
      <Section title='Filters' state={data.filters?.state}>
        <DataTable
          columns={
            [
              {
                header: 'Matches',
                cell: (f) => {
                  const c = filterCriteria(f)
                  return (
                    <ul className='space-y-0.5 text-xs break-all whitespace-normal'>
                      {c.length ? (
                        c.map((x) => <li key={x}>{x}</li>)
                      ) : (
                        <li>Every message</li>
                      )}
                    </ul>
                  )
                },
              },
              {
                header: 'Forwards to',
                cell: (f) => (
                  <span className='break-all'>{f.forwardTo || '—'}</span>
                ),
              },
              {
                header: 'Flags',
                cell: (f) => (
                  <div className='flex flex-wrap gap-1'>
                    {filterRisks(f).map((r) => (
                      <Badge
                        key={r.label}
                        variant={
                          r.tone === 'danger'
                            ? 'destructive'
                            : r.tone === 'warning'
                              ? 'secondary'
                              : 'outline'
                        }
                      >
                        {r.label}
                      </Badge>
                    ))}
                  </div>
                ),
              },
            ] as Column<
              NonNullable<GmailSettings['filters']>['filters'][number]
            >[]
          }
          data={data.filters?.filters}
          isLoading={false}
          emptyMessage='No filters'
        />
      </Section>
      <Section title='Send-as addresses' state={data.sendAs?.state}>
        <DataTable
          columns={
            [
              {
                header: 'Address',
                cell: (a) => (
                  <div>
                    <p className='break-all'>{a.email}</p>
                    {a.displayName && (
                      <p className='text-xs text-muted-foreground'>
                        {a.displayName}
                      </p>
                    )}
                  </div>
                ),
              },
              {
                header: 'Kind',
                cell: (a) =>
                  [a.isPrimary && 'Primary', a.isDefault && 'Default']
                    .filter(Boolean)
                    .join(', ') || 'Alias',
              },
              {
                header: 'Status',
                cell: (a) => a.verificationStatus || '—',
              },
              {
                header: 'Domain',
                cell: (a) => <ExposureBadge external={a.external} />,
              },
            ] as Column<
              NonNullable<GmailSettings['sendAs']>['addresses'][number]
            >[]
          }
          data={data.sendAs?.addresses}
          isLoading={false}
          emptyMessage='No send-as addresses'
        />
      </Section>
    </div>
  )
}

function VacationEditor({
  vacation,
  connectionId,
  tenantId,
  userId,
  access,
}: {
  vacation: GmailVacation
  connectionId: string
  tenantId: string
  userId: string
  access: { reason: string; targetEmail: string }
}) {
  const save = useWorkspaceGmailVacation(connectionId, tenantId, userId, access)
  const [draft, setDraft] = useState({
    enabled: vacation.enabled,
    subject: vacation.subject,
    body: vacation.bodyPlainText,
    start: toLocalInput(
      vacation.startTime ? timestampDate(vacation.startTime) : undefined
    ),
    end: toLocalInput(
      vacation.endTime ? timestampDate(vacation.endTime) : undefined
    ),
    restrictToContacts: vacation.restrictToContacts,
    restrictToDomain: vacation.restrictToDomain,
  })
  const [error, setError] = useState('')
  const set = <K extends keyof typeof draft>(k: K, v: (typeof draft)[K]) =>
    setDraft((d) => ({ ...d, [k]: v }))
  async function submit(e: FormEvent) {
    e.preventDefault()
    const problem = vacationError(draft)
    setError(problem)
    if (problem) return
    try {
      await save.mutateAsync({
        enabled: draft.enabled,
        subject: draft.subject,
        bodyPlainText: draft.body,
        restrictToContacts: draft.restrictToContacts,
        restrictToDomain: draft.restrictToDomain,
        startTime: fromLocalInput(draft.start),
        endTime: fromLocalInput(draft.end),
      })
      toast.success('Auto-reply saved')
    } catch (e) {
      // An unconfirmed change stays visible as an error: read again before retrying.
      setError(String(e))
    }
  }
  return (
    <form onSubmit={submit} className='space-y-3'>
      {vacation.bodyIsHtml && (
        <Alert>
          <AlertDescription>
            The stored message is formatted (HTML). Saving replaces it with the
            plain text below.
          </AlertDescription>
        </Alert>
      )}
      <div className='flex items-center gap-2'>
        <Switch
          id='gmail-vacation-enabled'
          checked={draft.enabled}
          onCheckedChange={(v) => set('enabled', v)}
        />
        <Label htmlFor='gmail-vacation-enabled'>
          {draft.enabled ? 'On' : 'Off'}
        </Label>
      </div>
      <Field
        id='gmail-vacation-subject'
        label='Subject'
        value={draft.subject}
        onChange={(v) => set('subject', v)}
      />
      <div className='space-y-2'>
        <Label htmlFor='gmail-vacation-body'>Message</Label>
        <Textarea
          id='gmail-vacation-body'
          rows={5}
          value={draft.body}
          onChange={(e) => set('body', e.target.value)}
        />
      </div>
      <div className='grid gap-3 sm:grid-cols-2'>
        <div className='space-y-2'>
          <Label htmlFor='gmail-vacation-start'>First day (optional)</Label>
          <Input
            id='gmail-vacation-start'
            type='datetime-local'
            value={draft.start}
            onChange={(e) => set('start', e.target.value)}
          />
        </div>
        <div className='space-y-2'>
          <Label htmlFor='gmail-vacation-end'>Last day (optional)</Label>
          <Input
            id='gmail-vacation-end'
            type='datetime-local'
            value={draft.end}
            onChange={(e) => set('end', e.target.value)}
          />
        </div>
      </div>
      <div className='flex flex-wrap gap-4 text-sm'>
        <label className='flex items-center gap-2'>
          <Checkbox
            checked={draft.restrictToContacts}
            onCheckedChange={(v) => set('restrictToContacts', v === true)}
          />
          Only reply to contacts
        </label>
        <label className='flex items-center gap-2'>
          <Checkbox
            checked={draft.restrictToDomain}
            onCheckedChange={(v) => set('restrictToDomain', v === true)}
          />
          Only reply inside the organization
        </label>
      </div>
      {error && (
        <p role='alert' className='text-sm text-destructive'>
          {error}
        </p>
      )}
      <Button disabled={save.isPending}>
        {save.isPending ? 'Saving…' : 'Save auto-reply'}
      </Button>
    </form>
  )
}
