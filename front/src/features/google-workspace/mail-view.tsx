import { useState, type FormEvent } from 'react'
import { timestampDate } from '@bufbuild/protobuf/wkt'
import { toast } from 'sonner'
import {
  useWorkspaceGmailAttachment,
  useWorkspaceGmailLabels,
  useWorkspaceGmailMessage,
  useWorkspaceGmailSearch,
  type GmailAttachment,
  type GmailMessageSummary,
  type GmailSearchParams,
} from '@/api/google-workspace'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { DataTable, type Column } from '@/components/data-table'
import { CursorPager, DirectoryError } from './directory-parts'
import { useCursor } from './directory-state'
import type { GmailConfirmation } from './gmail-access'
import {
  MAIL_SCOPE,
  attachmentBlocked,
  downloadName,
  formatBytes,
  htmlDocument,
  labelText,
  recipients,
} from './gmail-mail'
import type { Props } from './gmail-settings'
import { formatWorkspaceTime } from './observability-format'

type MailProps = Props & { confirmation: GmailConfirmation }

const when = (t: GmailMessageSummary['date']) =>
  t ? timestampDate(t).toLocaleString() : '—'

// The Mail view. Opening it reads the label list once; nothing else is sent
// until you press Search or open a message, and each of those is recorded with
// your reason. Switching to another tab drops what is shown.
export function MailView(props: MailProps) {
  const { connectionId, tenantId, userId, confirmation } = props
  const access = {
    reason: confirmation.reason,
    targetEmail: confirmation.targetEmail,
  }
  const nonce = confirmation.confirmedAt
  const labels = useWorkspaceGmailLabels(
    connectionId,
    tenantId,
    userId,
    access,
    nonce
  )
  const [query, setQuery] = useState('')
  const [labelId, setLabelId] = useState('')
  const [includeSpamTrash, setIncludeSpamTrash] = useState(false)
  const [applied, setApplied] = useState<Omit<
    GmailSearchParams,
    'pageToken'
  > | null>(null)
  const [open, setOpen] = useState<string | null>(null)
  const cursor = useCursor()
  const search = useWorkspaceGmailSearch(
    connectionId,
    tenantId,
    userId,
    access,
    nonce,
    applied ? { ...applied, pageToken: cursor.token } : null
  )

  function submit(e: FormEvent) {
    e.preventDefault()
    cursor.reset()
    setOpen(null)
    setApplied({
      query: query.trim(),
      labelIds: labelId ? [labelId] : [],
      includeSpamTrash,
    })
  }

  if (open !== null) {
    return (
      <MessageView
        {...props}
        access={access}
        messageId={open}
        onBack={() => setOpen(null)}
      />
    )
  }

  const columns: Column<GmailMessageSummary>[] = [
    {
      header: 'From',
      cell: (m) => <span className='break-all'>{m.from || '—'}</span>,
    },
    {
      header: 'Subject',
      cell: (m) =>
        m.unavailable ? (
          <span className='text-muted-foreground'>
            Gmail would not give this message’s headers
          </span>
        ) : (
          <div className='space-y-0.5 whitespace-normal'>
            <p className='font-medium break-words'>
              {m.subject || '(no subject)'}
              {m.hasAttachment && (
                <span className='ml-2 text-xs font-normal text-muted-foreground'>
                  📎
                </span>
              )}
            </p>
            <p className='text-xs break-words text-muted-foreground'>
              {m.snippet}
            </p>
          </div>
        ),
    },
    { header: 'Date', cell: (m) => when(m.date) },
    {
      header: '',
      cell: (m) => (
        <Button variant='outline' size='sm' onClick={() => setOpen(m.id)}>
          Open
        </Button>
      ),
    },
  ]

  return (
    <div className='space-y-4'>
      <DirectoryError error={labels.error} scope={MAIL_SCOPE} />
      <form onSubmit={submit} className='space-y-3'>
        <div className='flex flex-wrap items-end gap-3'>
          <div className='min-w-64 flex-1 space-y-1'>
            <Label htmlFor='mail-query'>Search</Label>
            <Input
              id='mail-query'
              value={query}
              maxLength={1000}
              placeholder='from:name@example.com has:attachment newer_than:7d'
              onChange={(e) =>
                setQuery(e.target.value.replace(/[\r\n]+/g, ' '))
              }
            />
          </div>
          <div className='space-y-1'>
            <Label htmlFor='mail-label'>Label</Label>
            <select
              id='mail-label'
              className='h-9 rounded-md border bg-transparent px-2 text-sm'
              value={labelId}
              onChange={(e) => setLabelId(e.target.value)}
            >
              <option value=''>All mail</option>
              {labels.data?.labels.map((l) => (
                <option key={l.id} value={l.id}>
                  {labelText(l)}
                </option>
              ))}
            </select>
          </div>
          <Button disabled={search.isFetching}>
            {search.isFetching ? 'Searching…' : 'Search'}
          </Button>
        </div>
        <div className='flex items-center gap-2 text-sm'>
          <input
            id='mail-spam'
            type='checkbox'
            checked={includeSpamTrash}
            onChange={(e) => setIncludeSpamTrash(e.target.checked)}
          />
          <Label htmlFor='mail-spam'>Include spam and trash</Label>
        </div>
        <p className='text-xs text-muted-foreground'>
          Uses Gmail’s search syntax; leave it empty to list recent mail. Each
          search, page turn and opened message is read from Gmail again and is
          recorded under Neo Box operations with your reason. The record names
          the search operators you used, never what you searched for.
        </p>
      </form>
      <DirectoryError error={search.error} scope={MAIL_SCOPE} />
      {search.isLoading && <p>Searching…</p>}
      {applied && !search.error && !search.isLoading && (
        <>
          <p className='text-xs text-muted-foreground'>
            Read {formatWorkspaceTime(search.data?.readAt)}. Gmail estimates
            about {search.data?.resultSizeEstimate ?? 0} matches in all.
          </p>
          <DataTable
            columns={columns}
            data={search.data?.messages}
            isLoading={false}
            emptyMessage='No messages match'
          />
          <CursorPager
            cursor={cursor}
            nextToken={search.data?.nextPageToken}
            busy={search.isFetching}
          />
        </>
      )}
    </div>
  )
}

function Row({ label, value }: { label: string; value: string }) {
  return (
    <>
      <dt className='text-muted-foreground'>{label}</dt>
      <dd className='break-all'>{value}</dd>
    </>
  )
}

function MessageView({
  connectionId,
  tenantId,
  userId,
  confirmation,
  messageId,
  access,
  onBack,
}: MailProps & {
  messageId: string
  access: { reason: string; targetEmail: string }
  onBack: () => void
}) {
  const q = useWorkspaceGmailMessage(
    connectionId,
    tenantId,
    userId,
    access,
    confirmation.confirmedAt,
    messageId
  )
  const download = useWorkspaceGmailAttachment(
    connectionId,
    tenantId,
    userId,
    access,
    messageId
  )
  const [showHtml, setShowHtml] = useState(false)
  const m = q.data?.message

  function save(a: GmailAttachment) {
    download.mutate(a.partId, {
      onSuccess: (file) => {
        const blob = new Blob([new Uint8Array(file.data)], {
          type: 'application/octet-stream',
        })
        const url = URL.createObjectURL(blob)
        const link = document.createElement('a')
        link.href = url
        link.download = downloadName(file.filename || a.filename)
        document.body.appendChild(link)
        link.click()
        link.remove()
        setTimeout(() => URL.revokeObjectURL(url), 10_000)
      },
      onError: (e) => toast.error(String(e)),
    })
  }

  const attachmentColumns: Column<GmailAttachment>[] = [
    {
      header: 'File',
      cell: (a) => (
        <span className='break-all'>
          {a.filename || '(no name)'}
          {a.inline && (
            <Badge variant='outline' className='ml-2'>
              Inline
            </Badge>
          )}
        </span>
      ),
    },
    { header: 'Type', cell: (a) => a.mimeType },
    { header: 'Size', cell: (a) => formatBytes(a.size) },
    {
      header: '',
      cell: (a) => {
        const blocked = attachmentBlocked(a)
        return blocked ? (
          <span className='text-xs text-muted-foreground'>{blocked}</span>
        ) : (
          <Button
            variant='outline'
            size='sm'
            disabled={download.isPending}
            onClick={() => save(a)}
            title='Downloads this attachment; the download is recorded'
          >
            Download
          </Button>
        )
      },
    },
  ]

  return (
    <div className='space-y-4'>
      <Button variant='outline' size='sm' onClick={onBack}>
        ← Back to results
      </Button>
      <DirectoryError error={q.error} scope={MAIL_SCOPE} />
      {q.isLoading && <p>Reading message…</p>}
      {m && (
        <>
          <section className='space-y-2 rounded-md border p-4'>
            <h3 className='text-lg font-semibold break-words'>
              {m.subject || '(no subject)'}
            </h3>
            <dl className='grid grid-cols-[6rem_1fr] gap-x-3 gap-y-1 text-sm'>
              <Row label='From' value={m.from || '—'} />
              <Row label='To' value={recipients(m.to)} />
              {m.cc && <Row label='Cc' value={m.cc} />}
              {m.bcc && <Row label='Bcc' value={m.bcc} />}
              {m.replyTo && <Row label='Reply-To' value={m.replyTo} />}
              <Row label='Date' value={when(m.date)} />
            </dl>
            <p className='text-xs text-muted-foreground'>
              Read {formatWorkspaceTime(q.data?.readAt)}. Opening a message does
              not mark it read.
            </p>
          </section>
          {m.bodyTruncated && (
            <Alert>
              <AlertDescription>
                This message is long; the body was cut at 2 MiB.
              </AlertDescription>
            </Alert>
          )}
          <section className='space-y-2 rounded-md border p-4'>
            <div className='flex items-center justify-between'>
              <h3 className='font-semibold'>Message</h3>
              {m.html && (
                <Button
                  variant='outline'
                  size='sm'
                  onClick={() => setShowHtml((v) => !v)}
                >
                  {showHtml ? 'Show plain text' : 'Show formatted (HTML)'}
                </Button>
              )}
            </div>
            {showHtml && m.html ? (
              <>
                <iframe
                  title='Message body'
                  sandbox=''
                  referrerPolicy='no-referrer'
                  srcDoc={htmlDocument(m.html)}
                  className='h-[480px] w-full rounded border bg-white'
                />
                <p className='text-xs text-muted-foreground'>
                  Shown without scripts, images or links, so opening it tells
                  the sender nothing.
                </p>
              </>
            ) : (
              <>
                {m.plainTextFromHtml && (
                  <p className='text-xs text-muted-foreground'>
                    This message has no plain-text part; this text was made from
                    its HTML.
                  </p>
                )}
                <pre className='max-h-[480px] overflow-auto text-sm break-words whitespace-pre-wrap'>
                  {m.plainText || '(empty)'}
                </pre>
              </>
            )}
          </section>
          <section className='space-y-2 rounded-md border p-4'>
            <h3 className='font-semibold'>Attachments</h3>
            <DataTable
              columns={attachmentColumns}
              data={m.attachments}
              isLoading={false}
              emptyMessage='No attachments'
            />
          </section>
        </>
      )}
    </div>
  )
}
