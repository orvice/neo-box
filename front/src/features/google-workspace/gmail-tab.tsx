import { useEffect, useState } from 'react'
import { Button } from '@/components/ui/button'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { AccessForm } from './access-form'
import { confirmationRemainingMs, confirmationUsable } from './gmail-access'
import { SettingsView, type Props } from './gmail-settings'
import { MailView } from './mail-view'

function useNow(intervalMs: number) {
  const [now, setNow] = useState(() => Date.now())
  useEffect(() => {
    const id = setInterval(() => setNow(Date.now()), intervalMs)
    return () => clearInterval(id)
  }, [intervalMs])
  return now
}

// A user's Gmail tab: one confirmation (reason and the user's email typed
// again) covers both the settings and the mail views for ten minutes. The
// views not shown are not mounted, so nothing is read, or recorded, until you
// open it (docs/adr/0008, 0010).
export function UserGmail(props: Props) {
  const now = useNow(15_000)
  const { confirmation } = props
  if (!confirmationUsable(confirmation, props.userEmail, now)) {
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
      <Tabs defaultValue='mail'>
        <TabsList>
          <TabsTrigger value='mail'>Mail</TabsTrigger>
          <TabsTrigger value='settings'>Settings</TabsTrigger>
        </TabsList>
        <TabsContent value='mail' className='pt-4'>
          <MailView {...props} confirmation={confirmation} />
        </TabsContent>
        <TabsContent value='settings' className='pt-4'>
          <SettingsView {...props} confirmation={confirmation} />
        </TabsContent>
      </Tabs>
    </div>
  )
}
