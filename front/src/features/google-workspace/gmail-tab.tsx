import { useEffect, useState } from 'react'
import { useWorkspaceGmailLabels } from '@/api/google-workspace'
import { Button } from '@/components/ui/button'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { AccessForm } from './access-form'
import { confirmationRemainingMs, confirmationUsable } from './gmail-access'
import { SettingsView, type Props } from './gmail-settings'
import { MailView } from './mail-view'
import { MailboxStats } from './mailbox-stats'
import { UserUsagePanel } from './user-usage-panel'

function useNow(intervalMs: number) {
  const [now, setNow] = useState(() => Date.now())
  useEffect(() => {
    const id = setInterval(() => setNow(Date.now()), intervalMs)
    return () => clearInterval(id)
  }, [intervalMs])
  return now
}

// A user's Gmail tab. Storage & activity comes from Google's aggregate usage
// reports and needs no mailbox access, so it opens first. The Mailbox view
// reads the mailbox itself and stays behind the access confirmation.
export function UserGmail(props: Props) {
  return (
    <Tabs defaultValue='usage'>
      <TabsList>
        <TabsTrigger value='usage'>Storage &amp; activity</TabsTrigger>
        <TabsTrigger value='mailbox'>Mailbox</TabsTrigger>
      </TabsList>
      <TabsContent value='usage' className='pt-4'>
        <UserUsagePanel
          connectionId={props.connectionId}
          tenantId={props.tenantId}
          userId={props.userId}
        />
      </TabsContent>
      <TabsContent value='mailbox' className='pt-4'>
        <Mailbox {...props} />
      </TabsContent>
    </Tabs>
  )
}

// One confirmation (reason and the user's email typed again) covers the
// overview, the mail and the settings views for ten minutes. The views not
// shown are not mounted, so nothing is read, or recorded, until you open it
// (docs/adr/0008, 0010). The label read is shared by the overview and the
// mail view, so moving between them does not send, or record, it again.
function Mailbox(props: Props) {
  const now = useNow(15_000)
  const { confirmation } = props
  const [view, setView] = useState('overview')
  const usable = confirmationUsable(confirmation, props.userEmail, now)
  const labels = useWorkspaceGmailLabels(
    props.connectionId,
    props.tenantId,
    props.userId,
    usable && confirmation
      ? { reason: confirmation.reason, targetEmail: confirmation.targetEmail }
      : null,
    confirmation?.confirmedAt ?? 0,
    view === 'overview' || view === 'mail'
  )
  if (!usable || !confirmation) {
    return (
      <AccessForm
        id='gmail'
        subject='this mailbox'
        userEmail={props.userEmail}
        expired={confirmation !== null}
        submitLabel='Access this mailbox'
        onConfirm={props.onConfirmation}
        description={
          <>
            <p>
              Neo Box reads this mailbox as{' '}
              <span className='break-all'>{props.userEmail}</span>: its{' '}
              <strong>settings</strong> (auto-reply, forwarding, delegates,
              filters, send-as) and its <strong>mail</strong> (labels, messages
              and attachments). It only reads mail; it never changes or sends
              anything in it.
            </p>
            <p className='mt-2'>
              Each access is recorded under Neo Box operations with your reason.
              The record holds structure only, never subjects, bodies, addresses
              or file names.
            </p>
          </>
        }
      />
    )
  }
  const minutes = Math.ceil(confirmationRemainingMs(confirmation, now) / 60000)
  return (
    <div className='space-y-4'>
      <div className='flex flex-wrap items-center justify-between gap-3 text-sm'>
        <p className='text-muted-foreground'>
          Reason: <em>{confirmation.reason}</em> · access ends in about{' '}
          {minutes} min
        </p>
        <Button
          variant='outline'
          size='sm'
          onClick={() => props.onConfirmation(null)}
        >
          End access
        </Button>
      </div>
      <Tabs value={view} onValueChange={setView}>
        <TabsList>
          <TabsTrigger value='overview'>Overview</TabsTrigger>
          <TabsTrigger value='mail'>Mail</TabsTrigger>
          <TabsTrigger value='settings'>Settings</TabsTrigger>
        </TabsList>
        <TabsContent value='overview' className='pt-4'>
          <MailboxStats labels={labels} />
        </TabsContent>
        <TabsContent value='mail' className='pt-4'>
          <MailView {...props} confirmation={confirmation} labels={labels} />
        </TabsContent>
        <TabsContent value='settings' className='pt-4'>
          <SettingsView {...props} confirmation={confirmation} />
        </TabsContent>
      </Tabs>
    </div>
  )
}
