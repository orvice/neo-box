import { useState } from 'react'
import { CircleAlert, Mail } from 'lucide-react'
import {
  CloudflareResourceKind,
  CloudflareEmailAction as Action,
  useCloudflareEmailAddresses,
  useCloudflareEmailChanges,
  useCloudflareEmailRouting,
  type CloudflareEmailCatchAll,
  type CloudflareEmailDNSRecord,
  type CloudflareEmailRoutingSettings,
  type CloudflareReadIssue,
} from '@/api/cloudflare'
import { cn } from '@/lib/utils'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { Skeleton } from '@/components/ui/skeleton'
import { changeError } from './access'
import { toastChange } from './changed'
import {
  catchAllConfigurable,
  catchAllMode,
  catchAllProblem,
  catchAllRequest,
  catchAllSummary,
  dnsRecordSummary,
  routingState,
  verifiedEmails,
  type CatchAllMode,
} from './email'
import { AddressesSection } from './email-addresses'
import { RulesSection } from './email-rules'
import { ProblemAlert } from './list-parts'
import { ConfirmChangeDialog } from './manage-dialogs'
import { ResourceOperationsLog } from './resource-operations'

/**
 * A Zone's Email Routing: whether it is on, the routing rules, the
 * catch-all, the Account's destination addresses and the log of changes.
 */
export function EmailRoutingTab({
  connectionId,
  zoneId,
  zoneName,
}: {
  connectionId: string
  zoneId: string
  zoneName: string
}) {
  const routing = useCloudflareEmailRouting(connectionId, zoneId)
  const addresses = useCloudflareEmailAddresses(connectionId)

  if (routing.error) {
    return (
      <ProblemAlert
        error={routing.error}
        permission='Email Routing Rules · Read'
      />
    )
  }
  const data = routing.data
  if (!data) return <Skeleton className='h-24 w-full' />
  const enabled = data.settings?.enabled ?? false
  const catchAll = data.catchAll

  return (
    <div className='space-y-6'>
      <StatusSection
        connectionId={connectionId}
        zoneId={zoneId}
        zoneName={zoneName}
        settings={data.settings}
        records={data.requiredDnsRecords}
        issues={data.issues}
      />
      {enabled && (
        <>
          {catchAll && (
            <CatchAllSection
              key={`${catchAll.enabled}-${catchAll.action}-${catchAll.target}`}
              connectionId={connectionId}
              zoneId={zoneId}
              catchAll={catchAll}
              verified={verifiedEmails(addresses.data?.addresses ?? [])}
            />
          )}
          <RulesSection
            connectionId={connectionId}
            zoneId={zoneId}
            zoneName={zoneName}
            rules={data.rules}
            verified={verifiedEmails(addresses.data?.addresses ?? [])}
          />
        </>
      )}
      <AddressesSection
        connectionId={connectionId}
        zoneId={zoneId}
        addresses={addresses.data?.addresses}
        error={addresses.error}
        loading={addresses.isLoading}
      />
      <section className='space-y-2'>
        <h3 className='text-sm font-medium'>Changes made from Neo Box</h3>
        <ResourceOperationsLog
          connectionId={connectionId}
          kind={CloudflareResourceKind.EMAIL_ROUTING}
          resource=''
        />
      </section>
    </div>
  )
}

function StatusSection({
  connectionId,
  zoneId,
  zoneName,
  settings,
  records,
  issues,
}: {
  connectionId: string
  zoneId: string
  zoneName: string
  settings: CloudflareEmailRoutingSettings | undefined
  records: CloudflareEmailDNSRecord[]
  issues: CloudflareReadIssue[]
}) {
  const changes = useCloudflareEmailChanges(connectionId, zoneId)
  const [enabling, setEnabling] = useState(false)
  const enabled = settings?.enabled ?? false
  const state = routingState(settings)

  return (
    <div className='space-y-3'>
      <div className='flex flex-wrap items-center justify-between gap-3 rounded-md border p-4'>
        <div className='space-y-1'>
          <div className='flex items-center gap-2'>
            <Mail className='h-4 w-4' />
            <span className='font-medium'>Email Routing</span>
            <Badge className={cn(state.className)}>{state.label}</Badge>
          </div>
          <p className='text-sm text-muted-foreground'>{state.hint}</p>
        </div>
        {!enabled && (
          <Button onClick={() => setEnabling(true)}>
            Enable Email Routing
          </Button>
        )}
      </div>
      {issues.map((i) => (
        <Alert key={i.source}>
          <CircleAlert />
          <AlertTitle>Could not read the {i.source}</AlertTitle>
          <AlertDescription>{i.message}</AlertDescription>
        </Alert>
      ))}
      {enabling && (
        <ConfirmChangeDialog
          title={`Enable Email Routing for ${zoneName}?`}
          description='Cloudflare adds these DNS records and locks them. Mail to this zone then goes to Cloudflare, so any other mail service on the zone stops receiving it.'
          confirmLabel='Enable'
          pendingLabel='Enabling…'
          destructive={false}
          what='Email Routing settings'
          run={async () => {
            const res = await changes.enable.mutateAsync()
            toastChange('Email Routing is enabled', res.result)
          }}
          onClose={() => setEnabling(false)}
        >
          {records.length > 0 && (
            <ul className='space-y-1 rounded-md border p-3 font-mono text-xs break-all'>
              {records.map((r) => (
                <li key={`${r.type}-${r.content}`}>{dnsRecordSummary(r)}</li>
              ))}
            </ul>
          )}
        </ConfirmChangeDialog>
      )}
    </div>
  )
}

function CatchAllSection({
  connectionId,
  zoneId,
  catchAll,
  verified,
}: {
  connectionId: string
  zoneId: string
  catchAll: CloudflareEmailCatchAll
  verified: string[]
}) {
  const changes = useCloudflareEmailChanges(connectionId, zoneId)
  const configurable = catchAllConfigurable(catchAll)
  const initial = catchAllMode(catchAll)
  const initialTarget =
    catchAll.action === Action.FORWARD ? catchAll.target : ''
  const [mode, setMode] = useState<CatchAllMode>(initial)
  const [target, setTarget] = useState(initialTarget)
  const [problem, setProblem] = useState<string>()
  const [failure, setFailure] = useState<string>()

  const dirty =
    mode !== initial || (mode === 'forward' && target.trim() !== initialTarget)

  function save() {
    const p = catchAllProblem(mode, target)
    setProblem(p)
    if (p) return
    setFailure(undefined)
    changes.setCatchAll.mutate(catchAllRequest(mode, target), {
      onSuccess: (res) => toastChange('Catch-all saved', res.result),
      onError: (err) => setFailure(changeError(err, 'catch-all rule').message),
    })
  }

  return (
    <section className='space-y-2'>
      <h3 className='text-sm font-medium'>Catch-all</h3>
      <div className='space-y-3 rounded-md border p-4'>
        <p className='text-sm text-muted-foreground'>
          What happens to mail for addresses no rule matches. Now:{' '}
          <span className='font-medium text-foreground'>
            {catchAllSummary(catchAll)}
          </span>
        </p>
        {configurable ? (
          <div className='flex flex-col gap-2 sm:flex-row sm:items-start'>
            <Select
              value={mode}
              onValueChange={(v) => setMode(v as CatchAllMode)}
            >
              <SelectTrigger className='w-full sm:w-48' aria-label='Catch-all'>
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value='off'>Off</SelectItem>
                <SelectItem value='drop'>Drop</SelectItem>
                <SelectItem value='forward'>Forward to</SelectItem>
              </SelectContent>
            </Select>
            {mode === 'forward' && (
              <div className='w-full space-y-1 sm:max-w-sm'>
                <Input
                  value={target}
                  onChange={(e) => setTarget(e.target.value)}
                  placeholder='me@gmail.com'
                  className='font-mono'
                  aria-label='Catch-all destination address'
                  autoComplete='off'
                />
                <p className='text-xs text-muted-foreground'>
                  {verified.length > 0
                    ? `Verified: ${verified.join(', ')}`
                    : 'No verified destination address yet.'}
                </p>
              </div>
            )}
            <Button
              onClick={save}
              disabled={!dirty || changes.setCatchAll.isPending}
            >
              {changes.setCatchAll.isPending ? 'Saving…' : 'Save'}
            </Button>
          </div>
        ) : (
          <p className='text-xs text-muted-foreground'>
            Read-only. {catchAll.readonlyReason}
          </p>
        )}
        {problem && <p className='text-xs text-danger-foreground'>{problem}</p>}
        {failure && (
          <p className='text-sm break-words text-danger-foreground'>
            {failure}
          </p>
        )}
      </div>
    </section>
  )
}
