import { useMemo } from 'react'
import type { useWorkspaceGmailLabels } from '@/api/google-workspace'
import { formatCount } from '@/lib/format'
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from '@/components/ui/card'
import { Skeleton } from '@/components/ui/skeleton'
import { DirectoryError } from './directory-parts'
import { MAIL_SCOPE } from './gmail-mail'
import { formatWorkspaceTime } from './observability-format'
import { mailboxStats, type LabelCount } from './user-usage'

export type LabelsQuery = ReturnType<typeof useWorkspaceGmailLabels>

const SHOWN_USER_LABELS = 15

/**
 * What is in the mailbox right now, from the label counts Gmail keeps: how much
 * is in the inbox, unread, sent, spam and trash, and which of the user's own
 * labels hold the most. It shares the one recorded label read with the Mail
 * view, and counts Gmail would not give are left out, not shown as zero.
 */
export function MailboxStats({ labels }: { labels: LabelsQuery }) {
  const stats = useMemo(
    () => mailboxStats(labels.data?.labels ?? []),
    [labels.data]
  )
  if (labels.isLoading) return <Skeleton className='h-48' />
  if (labels.error)
    return <DirectoryError error={labels.error} scope={MAIL_SCOPE} />
  const figures: {
    label: string
    l?: LabelCount
    value?: number
    note?: string
  }[] = [
    {
      label: 'Inbox',
      l: stats.inbox,
      note: stats.inbox
        ? `${formatCount(stats.inbox.messagesUnread)} unread`
        : undefined,
    },
    { label: 'Unread, all mail', value: stats.unread },
    { label: 'Sent', l: stats.sent },
    { label: 'Drafts', l: stats.drafts },
    { label: 'Spam', l: stats.spam },
    { label: 'Trash', l: stats.trash },
    { label: 'Starred', l: stats.starred },
  ]
  const max = Math.max(1, ...stats.userLabels.map((l) => l.messagesTotal))
  const shown = stats.userLabels.slice(0, SHOWN_USER_LABELS)

  return (
    <div className='space-y-4'>
      <div className='grid gap-4 sm:grid-cols-2 lg:grid-cols-4'>
        {figures.map((f) => {
          const n = f.value ?? f.l?.messagesTotal
          return (
            <Card key={f.label} className='gap-1 py-4'>
              <CardHeader className='px-4'>
                <CardDescription>{f.label}</CardDescription>
                <CardTitle className='text-2xl tabular-nums'>
                  {n === undefined ? '—' : formatCount(n)}
                </CardTitle>
              </CardHeader>
              <CardContent className='px-4 text-xs text-muted-foreground'>
                {n === undefined
                  ? 'Not reported for this mailbox'
                  : (f.note ?? 'messages')}
              </CardContent>
            </Card>
          )
        })}
      </div>
      <Card>
        <CardHeader>
          <CardTitle>Largest labels</CardTitle>
          <CardDescription>
            The user’s own labels by message count
            {stats.userLabels.length > shown.length
              ? `, top ${shown.length} of ${stats.userLabels.length}`
              : ''}
            .
            {stats.unknownUserLabels > 0 &&
              ` ${stats.unknownUserLabels} label${stats.unknownUserLabels === 1 ? '' : 's'} without a readable count ${stats.unknownUserLabels === 1 ? 'is' : 'are'} left out.`}
          </CardDescription>
        </CardHeader>
        <CardContent>
          {shown.length === 0 ? (
            <p className='text-sm text-muted-foreground'>
              This mailbox has no labels of its own.
            </p>
          ) : (
            <ul className='space-y-2'>
              {shown.map((l) => (
                <li key={l.id} className='space-y-1'>
                  <div className='flex items-baseline justify-between gap-3 text-sm'>
                    <span className='min-w-0 truncate' title={l.name}>
                      {l.name}
                    </span>
                    <span className='shrink-0 text-muted-foreground tabular-nums'>
                      {formatCount(l.messagesTotal)}
                      {l.messagesUnread > 0 &&
                        ` · ${formatCount(l.messagesUnread)} unread`}
                    </span>
                  </div>
                  <div className='h-1.5 overflow-hidden rounded-full bg-muted'>
                    <div
                      className='h-full rounded-full bg-primary'
                      style={{ width: `${(l.messagesTotal / max) * 100}%` }}
                    />
                  </div>
                </li>
              ))}
            </ul>
          )}
        </CardContent>
      </Card>
      <p className='text-xs text-muted-foreground'>
        Read {formatWorkspaceTime(labels.data?.readAt)}. This read is recorded
        under Neo Box operations with your reason; the record holds the number
        of labels, never their names.
      </p>
    </div>
  )
}
