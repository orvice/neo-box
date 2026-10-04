import { useState } from 'react'
import { toast } from 'sonner'
import {
  useCreateNotificationChannel,
  useUpdateNotificationChannel,
  type NotificationChannel,
} from '@/api/notifications'
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { PasswordInput } from '@/components/password-input'

/** Adds a Telegram channel, or edits one (an empty token keeps it). */
export function ChannelDialog({
  channel,
  onClose,
}: {
  channel?: NotificationChannel
  onClose: () => void
}) {
  const create = useCreateNotificationChannel()
  const update = useUpdateNotificationChannel()
  const [name, setName] = useState(channel?.name ?? 'Telegram')
  const [chatId, setChatId] = useState(
    channel?.config.case === 'telegram' ? channel.config.value.chatId : ''
  )
  const [botToken, setBotToken] = useState('')
  const pending = create.isPending || update.isPending
  const valid =
    name.trim() !== '' && chatId.trim() !== '' && (!!channel || botToken !== '')

  function handleSave() {
    const input = {
      name: name.trim(),
      chatId: chatId.trim(),
      botToken: botToken.trim(),
    }
    const done = {
      onSuccess: () => {
        toast.success(channel ? 'Channel saved' : 'Channel added')
        onClose()
      },
      onError: (e: Error) => toast.error(e.message),
    }
    if (channel) {
      update.mutate(
        { ...input, id: channel.id, enabled: channel.enabled },
        done
      )
    } else {
      create.mutate(input, done)
    }
  }

  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>
            {channel ? `Edit “${channel.name}”` : 'Add a Telegram channel'}
          </DialogTitle>
          <DialogDescription>
            Alerts are sent by your own bot to a chat you choose.
          </DialogDescription>
        </DialogHeader>
        <div className='space-y-5'>
          <div className='space-y-2'>
            <Label htmlFor='channel-name'>Name</Label>
            <Input
              id='channel-name'
              value={name}
              onChange={(e) => setName(e.target.value)}
            />
          </div>
          <div className='space-y-2'>
            <Label htmlFor='channel-token'>Bot token</Label>
            <PasswordInput
              id='channel-token'
              value={botToken}
              placeholder={
                channel
                  ? 'Leave empty to keep the current token'
                  : '123456:ABC-DEF…'
              }
              onChange={(e) => setBotToken(e.target.value)}
            />
          </div>
          <div className='space-y-2'>
            <Label htmlFor='channel-chat'>Chat ID</Label>
            <Input
              id='channel-chat'
              className='font-mono'
              value={chatId}
              placeholder='123456789, -1001234567890 or @channel'
              onChange={(e) => setChatId(e.target.value)}
            />
          </div>
          <ol className='list-decimal space-y-1 ps-5 text-xs text-muted-foreground'>
            <li>
              In Telegram, message <span className='font-mono'>@BotFather</span>{' '}
              with <span className='font-mono'>/newbot</span> and copy the token
              it gives you.
            </li>
            <li>
              Send your bot any message (or add it to a group and mention it).
            </li>
            <li>
              Open{' '}
              <span className='font-mono break-all'>
                https://api.telegram.org/bot&lt;token&gt;/getUpdates
              </span>{' '}
              and copy <span className='font-mono'>chat.id</span>. Group IDs are
              negative.
            </li>
          </ol>
        </div>
        <DialogFooter>
          <Button variant='outline' onClick={onClose}>
            Cancel
          </Button>
          <Button onClick={handleSave} disabled={!valid || pending}>
            {channel ? 'Save' : 'Add channel'}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
