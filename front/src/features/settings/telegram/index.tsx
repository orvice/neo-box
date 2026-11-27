import { useEffect, useRef, useState } from 'react'
import { Bot, Check, Copy, ExternalLink, Link2, Unlink } from 'lucide-react'
import { toast } from 'sonner'
import {
  useCreateTelegramLinkToken,
  useTelegramBotStatus,
  useUnbindTelegram,
  type CreateTelegramLinkTokenResponse,
} from '@/api/telegram'
import { formatTime } from '@/lib/format'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Skeleton } from '@/components/ui/skeleton'
import { ConfirmDialog } from '@/components/confirm-dialog'
import { EmptyState } from '@/components/empty-state'
import { ContentSection } from '../components/content-section'

const botCommands = [
  {
    command: '/connections',
    desc: 'Browse Connections by provider and view non-secret details',
  },
  {
    command: '/status',
    desc: 'View Connection health summary and failing Connections',
  },
  {
    command: '/unbind',
    desc: 'Disconnect this Telegram account from Neo Box',
  },
  { command: '/help', desc: 'List available commands' },
]

export function SettingsTelegram() {
  const [linkToken, setLinkToken] =
    useState<CreateTelegramLinkTokenResponse | null>(null)
  const [copied, setCopied] = useState(false)
  const [confirmUnbind, setConfirmUnbind] = useState(false)
  const awaitingLinkRef = useRef(false)

  const status = useTelegramBotStatus({ poll: !!linkToken })
  const createLink = useCreateTelegramLinkToken()
  const unbind = useUnbindTelegram()

  const binding = status.data?.binding
  const activeLink = binding ? null : linkToken

  useEffect(() => {
    if (binding && awaitingLinkRef.current) {
      awaitingLinkRef.current = false
      toast.success('Telegram account connected')
    }
  }, [binding])

  function handleGenerateLink() {
    createLink.mutate(undefined, {
      onSuccess: (res) => {
        awaitingLinkRef.current = true
        setLinkToken(res)
        setCopied(false)
      },
      onError: (e) => toast.error(e.message),
    })
  }

  async function handleCopyCommand(cmd: string) {
    try {
      await navigator.clipboard.writeText(cmd)
      setCopied(true)
      toast.success('Command copied')
      setTimeout(() => setCopied(false), 2000)
    } catch {
      toast.error('Failed to copy command')
    }
  }

  return (
    <ContentSection
      title='Telegram Bot'
      desc='Link a Telegram private chat to view your Neo Box Connections and their health status.'
    >
      <div className='space-y-8'>
        {status.isLoading ? (
          <Skeleton className='h-32' />
        ) : status.error ? (
          <div className='text-sm text-danger-foreground'>
            {status.error.message}
          </div>
        ) : !status.data?.enabled ? (
          <EmptyState
            icon={<Bot />}
            title='Telegram Bot is not configured'
            description='Ask your server administrator to set telegram.bot_token, telegram.webhook_url, and telegram.webhook_secret in the Neo Box configuration.'
          />
        ) : binding ? (
          <section className='space-y-4'>
            <div className='flex flex-wrap items-center justify-between gap-4 rounded-md border p-4'>
              <div className='space-y-1'>
                <div className='flex items-center gap-2'>
                  <span className='font-medium'>
                    {binding.displayName ||
                      (binding.telegramUsername
                        ? `@${binding.telegramUsername}`
                        : `User ${binding.telegramUserId.toString()}`)}
                  </span>
                  <Badge variant='secondary'>Connected</Badge>
                </div>
                <div className='text-xs text-muted-foreground'>
                  {binding.telegramUsername
                    ? `@${binding.telegramUsername} · `
                    : ''}
                  ID {binding.telegramUserId.toString()}
                  {binding.linkedAt
                    ? ` · Linked ${formatTime(binding.linkedAt)}`
                    : ''}
                </div>
              </div>
              <div className='flex items-center gap-2'>
                {status.data.botUsername && (
                  <Button variant='outline' size='sm' asChild>
                    <a
                      href={`https://t.me/${status.data.botUsername}`}
                      target='_blank'
                      rel='noreferrer'
                    >
                      <ExternalLink />
                      Open Bot
                    </a>
                  </Button>
                )}
                <Button
                  variant='outline'
                  size='sm'
                  onClick={() => setConfirmUnbind(true)}
                >
                  <Unlink />
                  Disconnect
                </Button>
              </div>
            </div>

            <div className='space-y-2'>
              <h4 className='text-sm font-medium'>Available commands</h4>
              <ul className='divide-y rounded-md border text-sm'>
                {botCommands.map((c) => (
                  <li
                    key={c.command}
                    className='flex flex-wrap items-baseline justify-between gap-2 px-3 py-2'
                  >
                    <code className='font-mono text-xs'>{c.command}</code>
                    <span className='text-xs text-muted-foreground'>
                      {c.desc}
                    </span>
                  </li>
                ))}
              </ul>
            </div>
          </section>
        ) : (
          <section className='space-y-4'>
            <div className='flex flex-wrap items-center justify-between gap-4 rounded-md border p-4'>
              <div className='space-y-1'>
                <div className='font-medium'>Connect your Telegram account</div>
                <p className='text-sm text-muted-foreground'>
                  Generate a one-time link and open it in a private chat with{' '}
                  {status.data.botUsername ? (
                    <span className='font-mono'>
                      @{status.data.botUsername}
                    </span>
                  ) : (
                    'the Neo Box bot'
                  )}
                  . Group chats are ignored.
                </p>
              </div>
              <Button
                size='sm'
                disabled={createLink.isPending}
                onClick={handleGenerateLink}
              >
                <Link2 />
                {activeLink ? 'Regenerate link' : 'Connect Telegram'}
              </Button>
            </div>

            {activeLink && (
              <div className='space-y-3 rounded-md border bg-muted/30 p-4'>
                <div className='flex flex-wrap items-center justify-between gap-2'>
                  <div className='text-sm font-medium'>
                    Waiting for confirmation in Telegram…
                  </div>
                  {activeLink.expiresAt && (
                    <span className='text-xs text-muted-foreground'>
                      Expires {formatTime(activeLink.expiresAt)}
                    </span>
                  )}
                </div>
                <div className='flex flex-wrap items-center gap-2'>
                  {activeLink.deepLink && (
                    <Button size='sm' asChild>
                      <a
                        href={activeLink.deepLink}
                        target='_blank'
                        rel='noreferrer'
                      >
                        <ExternalLink />
                        Open in Telegram
                      </a>
                    </Button>
                  )}
                  <Button
                    variant='outline'
                    size='sm'
                    onClick={() => handleCopyCommand(activeLink.command)}
                  >
                    {copied ? <Check /> : <Copy />}
                    Copy /start command
                  </Button>
                </div>
                <p className='text-xs text-muted-foreground'>
                  Or send{' '}
                  <code className='rounded bg-muted px-1 py-0.5 font-mono break-all'>
                    {activeLink.command}
                  </code>{' '}
                  directly in a private chat with the bot. This page updates
                  automatically once linked.
                </p>
              </div>
            )}
          </section>
        )}

        <ConfirmDialog
          open={confirmUnbind}
          onOpenChange={setConfirmUnbind}
          title='Disconnect Telegram?'
          desc='The Telegram bot will immediately stop showing your Connections until you link an account again.'
          destructive
          confirmText='Disconnect'
          isLoading={unbind.isPending}
          handleConfirm={() => {
            unbind.mutate(undefined, {
              onSuccess: () => {
                awaitingLinkRef.current = false
                setLinkToken(null)
                toast.success('Telegram account disconnected')
                setConfirmUnbind(false)
              },
              onError: (e) => toast.error(e.message),
            })
          }}
        />
      </div>
    </ContentSection>
  )
}
