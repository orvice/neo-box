import { useEffect, useState } from 'react'
import { timestampDate } from '@bufbuild/protobuf/wkt'
import {
  useWorkspaceSharedDrives,
  type WorkspaceSharedDrive,
} from '@/api/google-workspace'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Label } from '@/components/ui/label'
import { DataTable, type Column } from '@/components/data-table'
import { AccessForm } from './access-form'
import { CursorPager, DirectoryError, Field } from './directory-parts'
import { useCursor } from './directory-state'
import {
  DRIVE_READONLY_SCOPE,
  orgUnitIdError,
  parseMaxMembers,
  reasonUsable,
  restrictionLabels,
  type DriveConfirmation,
} from './drive-access'
import { CONFIRMATION_TTL_MS } from './gmail-access'

function useNow(intervalMs: number) {
  const [now, setNow] = useState(() => Date.now())
  useEffect(() => {
    const id = setInterval(() => setNow(Date.now()), intervalMs)
    return () => clearInterval(id)
  }, [intervalMs])
  return now
}

type Props = {
  connectionId: string
  tenantId: string
  confirmation: DriveConfirmation | null
  onConfirmation: (c: DriveConfirmation | null) => void
}

export function WorkspaceSharedDrives(props: Props) {
  const now = useNow(15_000)
  const { confirmation } = props
  if (!reasonUsable(confirmation, now)) {
    return (
      <AccessForm
        id='shared-drives'
        subject='the shared drive list'
        expired={confirmation !== null}
        submitLabel='List shared drives'
        onConfirm={props.onConfirmation}
        description={
          <>
            <p>
              Neo Box lists the domain’s shared drives with Google’s{' '}
              <code>drive.readonly</code> scope. That scope could read file
              content; Neo Box calls only the shared drive list, never opens or
              downloads a file, and does not read members. Each read is recorded
              under Neo Box operations with your reason.
            </p>
          </>
        }
      />
    )
  }
  return <DriveList {...props} confirmation={confirmation} now={now} />
}

function DriveList({
  connectionId,
  tenantId,
  confirmation,
  now,
  onConfirmation,
}: Props & { confirmation: DriveConfirmation; now: number }) {
  const [namePrefix, setNamePrefix] = useState('')
  const [noOrganizer, setNoOrganizer] = useState(false)
  const [maxText, setMaxText] = useState('')
  const [orgUnitId, setOrgUnitId] = useState('')
  const [applied, setApplied] = useState({
    namePrefix: '',
    noOrganizer: false,
    maxMembers: undefined as number | undefined,
    orgUnitId: '',
  })
  const cursor = useCursor()
  const max = parseMaxMembers(maxText)
  const ouError = orgUnitIdError(orgUnitId)
  const q = useWorkspaceSharedDrives(
    connectionId,
    tenantId,
    confirmation.reason,
    confirmation.confirmedAt,
    { ...applied, pageToken: cursor.token }
  )
  const minutes = Math.max(
    0,
    Math.ceil((CONFIRMATION_TTL_MS - (now - confirmation.confirmedAt)) / 60000)
  )
  const columns: Column<WorkspaceSharedDrive>[] = [
    { header: 'Name', cell: (d) => d.name || 'Not provided' },
    {
      header: 'Created',
      cell: (d) =>
        d.createdTime ? timestampDate(d.createdTime).toLocaleDateString() : '—',
    },
    {
      header: 'Org unit ID',
      cell: (d) => (
        <span className='font-mono text-xs break-all'>
          {d.orgUnitId || '—'}
        </span>
      ),
    },
    {
      header: 'Restrictions',
      cell: (d) => {
        const labels = restrictionLabels(d.restrictions)
        return labels.length ? (
          <div className='flex flex-wrap gap-1'>
            {labels.map((l) => (
              <Badge key={l} variant='outline'>
                {l}
              </Badge>
            ))}
          </div>
        ) : (
          <span className='text-muted-foreground'>None reported</span>
        )
      },
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
        className='space-y-3'
        onSubmit={(e) => {
          e.preventDefault()
          if (max.error || ouError) return
          setApplied({
            namePrefix: namePrefix.trim(),
            noOrganizer,
            maxMembers: max.value,
            orgUnitId: orgUnitId.trim(),
          })
          cursor.reset()
        }}
      >
        <div className='grid gap-3 sm:grid-cols-3'>
          <Field
            id='sd-name'
            label='Name starts with'
            value={namePrefix}
            onChange={setNamePrefix}
          />
          <Field
            id='sd-max'
            label='At most this many members'
            value={maxText}
            onChange={setMaxText}
          />
          <Field
            id='sd-ou'
            label='Org unit ID'
            value={orgUnitId}
            onChange={setOrgUnitId}
          />
        </div>
        {(max.error || ouError) && (
          <p role='alert' className='text-sm text-destructive'>
            {max.error || ouError}
          </p>
        )}
        <div className='flex flex-wrap items-center gap-4'>
          <div className='flex items-center gap-2'>
            <input
              id='sd-no-organizer'
              type='checkbox'
              checked={noOrganizer}
              onChange={(e) => setNoOrganizer(e.target.checked)}
            />
            <Label htmlFor='sd-no-organizer'>
              Only drives with no organizer
            </Label>
          </div>
          <Button>Apply filters</Button>
        </div>
      </form>
      <p className='text-xs text-muted-foreground'>
        Applying a filter or turning a page reads from Google again and is
        recorded. Members are not read; filters on member and organizer counts
        are evaluated by Google.
      </p>
      <DirectoryError error={q.error} scope={DRIVE_READONLY_SCOPE} />
      {q.isLoading && <p>Reading shared drives…</p>}
      {!q.error && !q.isLoading && (
        <>
          {q.data && q.data.drives.length === 0 && !q.data.nextPageToken && (
            <Alert>
              <AlertDescription>
                No shared drives match these filters.
              </AlertDescription>
            </Alert>
          )}
          <DataTable
            columns={columns}
            data={q.data?.drives}
            isLoading={false}
            emptyMessage='No shared drives on this page'
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
