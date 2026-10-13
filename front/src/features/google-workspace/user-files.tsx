import { useEffect, useState } from 'react'
import { timestampDate } from '@bufbuild/protobuf/wkt'
import {
  useWorkspaceExposedFiles,
  type WorkspaceExposedFile,
} from '@/api/google-workspace'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Label } from '@/components/ui/label'
import { DataTable, type Column } from '@/components/data-table'
import { AccessForm } from './access-form'
import { CursorPager, DirectoryError } from './directory-parts'
import { useCursor } from './directory-state'
import {
  DRIVE_METADATA_SCOPE,
  EXPOSURE_KINDS,
  exposureText,
  exposureTone,
  fileRisk,
  mimeLabel,
  safeLink,
  type DriveConfirmation,
  type Tone,
} from './drive-access'
import { CONFIRMATION_TTL_MS, confirmationUsable } from './gmail-access'

function useNow(intervalMs: number) {
  const [now, setNow] = useState(() => Date.now())
  useEffect(() => {
    const id = setInterval(() => setNow(Date.now()), intervalMs)
    return () => clearInterval(id)
  }, [intervalMs])
  return now
}

const badgeVariant = (t: Tone) =>
  t === 'danger' ? 'destructive' : t === 'warning' ? 'secondary' : 'outline'

type Props = {
  connectionId: string
  tenantId: string
  userId: string
  userEmail: string
  confirmation: DriveConfirmation | null
  onConfirmation: (c: DriveConfirmation | null) => void
}

export function UserExposedFiles(props: Props) {
  const now = useNow(15_000)
  const { confirmation } = props
  if (!confirmationUsable(confirmation, props.userEmail, now)) {
    return (
      <AccessForm
        id='user-files'
        subject='this user’s files'
        userEmail={props.userEmail}
        expired={confirmation !== null}
        submitLabel='List exposed files'
        onConfirm={props.onConfirmation}
        description={
          <p>
            Neo Box lists the files{' '}
            <span className='break-all'>{props.userEmail}</span> owns that are
            open to anyone, or to a whole domain, using Google’s{' '}
            <code>drive.metadata.readonly</code> scope as that user. It reads
            file names, types, dates and sharing; it never reads file content.
            Each read is recorded under Neo Box operations with your reason,
            without file names or links.
          </p>
        }
      />
    )
  }
  return <FileList {...props} confirmation={confirmation} now={now} />
}

function FileList({
  connectionId,
  tenantId,
  userId,
  confirmation,
  now,
  onConfirmation,
}: Props & { confirmation: DriveConfirmation; now: number }) {
  const [picked, setPicked] = useState<string[]>([])
  const [applied, setApplied] = useState<string[]>([])
  const cursor = useCursor()
  const access = {
    reason: confirmation.reason,
    targetEmail: confirmation.targetEmail,
  }
  const q = useWorkspaceExposedFiles(
    connectionId,
    tenantId,
    userId,
    access,
    confirmation.confirmedAt,
    { exposures: applied, pageToken: cursor.token }
  )
  const minutes = Math.max(
    0,
    Math.ceil((CONFIRMATION_TTL_MS - (now - confirmation.confirmedAt)) / 60000)
  )
  const columns: Column<WorkspaceExposedFile>[] = [
    {
      header: 'File',
      cell: (f) => {
        const link = safeLink(f.webViewLink)
        const name = f.name || 'Not provided'
        return link ? (
          <a
            href={link}
            target='_blank'
            rel='noopener noreferrer'
            className='break-all underline underline-offset-4'
          >
            {name}
          </a>
        ) : (
          <span className='break-all'>{name}</span>
        )
      },
    },
    { header: 'Type', cell: (f) => mimeLabel(f.mimeType) },
    {
      header: 'Modified',
      cell: (f) =>
        f.modifiedTime ? timestampDate(f.modifiedTime).toLocaleString() : '—',
    },
    {
      header: 'Exposure',
      cell: (f) =>
        f.exposures.length === 0 ? (
          <span className='text-muted-foreground'>
            Google did not return permissions
          </span>
        ) : (
          <div className='flex flex-col items-start gap-1'>
            {f.exposures.map((e, i) => (
              <Badge
                key={i}
                variant={badgeVariant(exposureTone(e))}
                title={fileRisk(f)}
              >
                {exposureText(e)}
              </Badge>
            ))}
          </div>
        ),
    },
  ]
  return (
    <div className='space-y-4'>
      <div className='flex flex-wrap items-center justify-between gap-3 text-sm'>
        <p className='text-muted-foreground'>
          Reason: <em>{confirmation.reason}</em> · access ends in about{' '}
          {minutes} min
        </p>
        <div className='flex gap-2'>
          <Button
            variant='outline'
            size='sm'
            disabled={q.isFetching}
            onClick={() => q.refetch()}
            title='Reads the list again; each read is recorded'
          >
            {q.isFetching ? 'Reading…' : 'Read again'}
          </Button>
          <Button
            variant='outline'
            size='sm'
            onClick={() => onConfirmation(null)}
          >
            End access
          </Button>
        </div>
      </div>
      <form
        className='flex flex-wrap items-center gap-4'
        onSubmit={(e) => {
          e.preventDefault()
          setApplied(picked)
          cursor.reset()
        }}
      >
        {EXPOSURE_KINDS.map((k) => (
          <div key={k.kind} className='flex items-center gap-2'>
            <input
              id={`exp-${k.kind}`}
              type='checkbox'
              checked={picked.includes(k.kind)}
              onChange={(e) =>
                setPicked((p) =>
                  e.target.checked
                    ? [...p, k.kind]
                    : p.filter((x) => x !== k.kind)
                )
              }
            />
            <Label htmlFor={`exp-${k.kind}`}>{k.label}</Label>
          </div>
        ))}
        <Button>Apply</Button>
      </form>
      <p className='text-xs text-muted-foreground'>
        Shows this user’s own, untrashed files; none ticked means all four
        kinds. Files in shared drives have no owner and are not listed. Applying
        a filter or turning a page reads from Google again and is recorded.
      </p>
      <DirectoryError error={q.error} scope={DRIVE_METADATA_SCOPE} />
      {q.isLoading && <p>Reading files…</p>}
      {!q.error && q.data?.incompleteSearch && (
        <Alert variant='destructive'>
          <AlertDescription>
            Google could not search everything; some files may be missing.
          </AlertDescription>
        </Alert>
      )}
      {!q.error && !q.isLoading && (
        <>
          <DataTable
            columns={columns}
            data={q.data?.files}
            isLoading={false}
            emptyMessage='No exposed files on this page'
          />
          <CursorPager
            cursor={cursor}
            nextToken={q.data?.nextPageToken}
            busy={q.isFetching}
          />
        </>
      )}
    </div>
  )
}
