import { useState } from 'react'
import {
  Bell,
  CircleAlert,
  Info,
  Pencil,
  Send,
  Trash2,
  TriangleAlert,
} from 'lucide-react'
import { toast } from 'sonner'
import {
  AlertSeverity,
  useAlerts,
  useDeleteNotificationChannel,
  useNotificationChannels,
  useTestNotificationChannel,
  useUpdateNotificationChannel,
  type Alert,
  type NotificationChannel,
} from '@/api/notifications'
import { formatTime } from '@/lib/format'
import { cn } from '@/lib/utils'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Skeleton } from '@/components/ui/skeleton'
import { Switch } from '@/components/ui/switch'
import { ConfirmDialog } from '@/components/confirm-dialog'
import { EmptyState } from '@/components/empty-state'
import { ContentSection } from '../components/content-section'
import { ChannelDialog } from './channel-dialog'

export function SettingsNotifications() {
  return (
    <ContentSection
      title='Notifications'
      desc='Where Neo Box tells you about failed scheduled snapshots, failed restores, and connections that stop working.'
    >
      <div className='space-y-10'>
        <Channels />
        <RecentAlerts />
      </div>
    </ContentSection>
  )
}

function Channels() {
  const channels = useNotificationChannels()
  const [editing, setEditing] = useState<NotificationChannel | 'new' | null>(
    null
  )
  const [deleting, setDeleting] = useState<NotificationChannel | null>(null)
  const remove = useDeleteNotificationChannel()

  return (
    <section className='space-y-4'>
      <div className='flex items-center justify-between gap-4'>
        <div>
          <h4 className='font-medium'>Telegram channels</h4>
          <p className='text-sm text-muted-foreground'>
            Every alert goes to each enabled channel.
          </p>
        </div>
        <Button size='sm' onClick={() => setEditing('new')}>
          <Send />
          Add channel
        </Button>
      </div>
      {channels.isLoading ? (
        <Skeleton className='h-20' />
      ) : channels.error ? (
        <div className='text-sm text-danger-foreground'>
          {channels.error.message}
        </div>
      ) : !channels.data?.length ? (
        <EmptyState
          title='No channels yet'
          description='Add a Telegram chat to get alerts outside the dashboard.'
        />
      ) : (
        <ul className='divide-y rounded-md border'>
          {channels.data.map((c) => (
            <ChannelRow
              key={c.id}
              channel={c}
              onEdit={() => setEditing(c)}
              onDelete={() => setDeleting(c)}
            />
          ))}
        </ul>
      )}

      {editing && (
        <ChannelDialog
          channel={editing === 'new' ? undefined : editing}
          onClose={() => setEditing(null)}
        />
      )}
      <ConfirmDialog
        open={!!deleting}
        onOpenChange={(open) => !open && setDeleting(null)}
        title='Delete channel?'
        desc={`Alerts will no longer be sent to "${deleting?.name}".`}
        destructive
        confirmText='Delete'
        isLoading={remove.isPending}
        handleConfirm={() => {
          if (!deleting) return
          remove.mutate(deleting.id, {
            onSuccess: () => {
              toast.success('Channel deleted')
              setDeleting(null)
            },
            onError: (e) => toast.error(e.message),
          })
        }}
      />
    </section>
  )
}

function ChannelRow({
  channel: c,
  onEdit,
  onDelete,
}: {
  channel: NotificationChannel
  onEdit: () => void
  onDelete: () => void
}) {
  const update = useUpdateNotificationChannel()
  const test = useTestNotificationChannel()
  const chatId = c.config.case === 'telegram' ? c.config.value.chatId : ''

  function setEnabled(enabled: boolean) {
    update.mutate(
      { id: c.id, name: c.name, enabled, chatId, botToken: '' },
      { onError: (e) => toast.error(e.message) }
    )
  }

  function sendTest() {
    test.mutate(c.id, {
      onSuccess: (error) =>
        error
          ? toast.error(`Test failed: ${error}`)
          : toast.success(`Test message sent to "${c.name}"`),
      onError: (e) => toast.error(e.message),
    })
  }

  return (
    <li className='flex flex-wrap items-center gap-3 p-3'>
      <Switch
        checked={c.enabled}
        onCheckedChange={setEnabled}
        disabled={update.isPending}
        aria-label={`Enable ${c.name}`}
      />
      <div className='min-w-0 flex-1'>
        <div className='flex items-center gap-2'>
          <span className='font-medium'>{c.name}</span>
          {!c.enabled && <Badge variant='secondary'>Off</Badge>}
        </div>
        <div className='font-mono text-xs text-muted-foreground'>
          chat {chatId}
        </div>
        {c.lastError ? (
          <div className='flex items-start gap-1 text-xs text-danger-foreground'>
            <CircleAlert className='mt-0.5 h-3 w-3 shrink-0' />
            <span className='break-words'>{c.lastError}</span>
          </div>
        ) : c.lastSentAt ? (
          <div className='text-xs text-muted-foreground'>
            Last sent {formatTime(c.lastSentAt)}
          </div>
        ) : null}
      </div>
      <div className='flex gap-1'>
        <Button
          size='sm'
          variant='outline'
          onClick={sendTest}
          disabled={test.isPending}
        >
          <Send />
          Test
        </Button>
        <Button size='sm' variant='ghost' onClick={onEdit} aria-label='Edit'>
          <Pencil />
        </Button>
        <Button
          size='sm'
          variant='ghost'
          onClick={onDelete}
          aria-label='Delete'
        >
          <Trash2 />
        </Button>
      </div>
    </li>
  )
}

const severityStyle: Record<
  AlertSeverity,
  { label: string; icon: typeof Info; className: string }
> = {
  [AlertSeverity.UNSPECIFIED]: {
    label: 'Alert',
    icon: Info,
    className: 'bg-muted text-muted-foreground',
  },
  [AlertSeverity.INFO]: {
    label: 'Info',
    icon: Info,
    className: 'bg-running-muted text-running-foreground',
  },
  [AlertSeverity.WARNING]: {
    label: 'Warning',
    icon: TriangleAlert,
    className: 'bg-warning-muted text-warning-foreground',
  },
  [AlertSeverity.CRITICAL]: {
    label: 'Critical',
    icon: CircleAlert,
    className: 'bg-danger-muted text-danger-foreground',
  },
}

function RecentAlerts() {
  const alerts = useAlerts()
  return (
    <section className='space-y-4'>
      <div>
        <h4 className='font-medium'>Recent alerts</h4>
        <p className='text-sm text-muted-foreground'>
          Each problem is reported once, however often it is seen.
        </p>
      </div>
      {alerts.isLoading ? (
        <Skeleton className='h-20' />
      ) : alerts.error ? (
        <div className='text-sm text-danger-foreground'>
          {alerts.error.message}
        </div>
      ) : !alerts.data?.length ? (
        <EmptyState
          icon={<Bell />}
          title='No alerts'
          description='Nothing has needed your attention yet.'
        />
      ) : (
        <ul className='divide-y rounded-md border'>
          {alerts.data.map((a) => (
            <AlertRow key={a.id} alert={a} />
          ))}
        </ul>
      )}
    </section>
  )
}

function AlertRow({ alert: a }: { alert: Alert }) {
  const style =
    severityStyle[a.severity] ?? severityStyle[AlertSeverity.UNSPECIFIED]
  const Icon = style.icon
  const delivered = !!a.deliveredAt
  return (
    <li className='space-y-1 p-3'>
      <div className='flex flex-wrap items-center gap-2'>
        <Badge className={cn('gap-1', style.className)}>
          <Icon className='h-3 w-3' />
          {style.label}
        </Badge>
        <span className='text-xs text-muted-foreground'>
          {formatTime(a.createdAt)}
        </span>
      </div>
      <div className='text-sm font-medium break-words'>
        {a.link ? (
          <a href={a.link} className='hover:underline'>
            {a.title}
          </a>
        ) : (
          a.title
        )}
      </div>
      {a.body && (
        <p className='line-clamp-3 text-xs whitespace-pre-line text-muted-foreground'>
          {a.body}
        </p>
      )}
      <div
        className={cn(
          'text-xs',
          delivered && !a.deliveryError
            ? 'text-muted-foreground'
            : 'text-danger-foreground'
        )}
      >
        {delivered
          ? `Sent ${formatTime(a.deliveredAt)}${a.deliveryError ? ` · some channels failed: ${a.deliveryError}` : ''}`
          : a.deliveryError
            ? `Not sent: ${a.deliveryError}`
            : 'Sending…'}
      </div>
    </li>
  )
}
