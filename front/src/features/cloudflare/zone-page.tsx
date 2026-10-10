import { useState } from 'react'
import { getRouteApi, Link } from '@tanstack/react-router'
import {
  ChevronLeft,
  Info,
  Pencil,
  Plus,
  ShieldAlert,
  Trash2,
} from 'lucide-react'
import { toast } from 'sonner'
import {
  useCloudflareDNSChanges,
  useCloudflareDNSRecords,
  useCloudflareZone,
  type CloudflareDNSRecord,
} from '@/api/cloudflare'
import { useConnection } from '@/api/connections'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import {
  AlertDialog,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from '@/components/ui/alert-dialog'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { Skeleton } from '@/components/ui/skeleton'
import { Switch } from '@/components/ui/switch'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { Page, PageHeader, PageScroll } from '@/components/common/page-parts'
import { DataTable, type Column } from '@/components/data-table'
import {
  changeError,
  dnsEditState,
  recordActions,
  type EditState,
} from './access'
import { toastChanged } from './changed'
import { EDITABLE_TYPES, contentSummary, formatTTL } from './dns'
import { SETUP_GUIDE } from './format'
import {
  ListFrame,
  Pager,
  ProblemAlert,
  RefreshButton,
  SearchBox,
} from './list-parts'
import { OperationsLog } from './operations'
import { RecordDialog } from './record-dialog'

const route = getRouteApi(
  '/_authenticated/connections/$connectionId/zones/$zoneId'
)

const ALL_TYPES = 'all'
const PAGE_SIZE = 50

/** A Zone's DNS records, with changes, and the log of changes made here. */
export function CloudflareZonePage() {
  const { connectionId, zoneId } = route.useParams()
  const connection = useConnection(connectionId)
  const zone = useCloudflareZone(connectionId, zoneId)
  const edit = dnsEditState(zone.data)
  const [adding, setAdding] = useState(false)

  return (
    <Page>
      <PageHeader
        breadcrumb={
          <Link
            to='/connections/$connectionId'
            params={{ connectionId }}
            className='inline-flex items-center gap-1'
          >
            <ChevronLeft className='h-3 w-3' />{' '}
            {connection.data?.name ?? 'Connection'}
          </Link>
        }
        title={
          zone.data?.name ??
          (zone.isLoading ? <Skeleton className='h-8 w-48' /> : 'Zone')
        }
        subtitle={
          zone.data && (
            <span className='inline-flex flex-wrap items-center gap-2'>
              <Badge variant='secondary'>{zone.data.status}</Badge>
              {zone.data.nameServers.length > 0 && (
                <span className='font-mono text-xs'>
                  {zone.data.nameServers.join(', ')}
                </span>
              )}
            </span>
          )
        }
        actions={
          <>
            <RefreshButton connectionId={connectionId} />
            <Button
              onClick={() => setAdding(true)}
              disabled={!edit.canEdit}
              title={edit.canEdit ? undefined : edit.reason}
            >
              <Plus className='h-4 w-4' /> Add record
            </Button>
          </>
        }
      />
      <PageScroll className='space-y-4'>
        {zone.error ? (
          <ProblemAlert error={zone.error} permission='Zone · Read' />
        ) : (
          <>
            <EditStateNotice state={edit} />
            <Tabs defaultValue='records'>
              <TabsList>
                <TabsTrigger value='records'>DNS records</TabsTrigger>
                <TabsTrigger value='operations'>Operation log</TabsTrigger>
              </TabsList>
              <TabsContent value='records' className='pt-2'>
                <RecordsTab
                  connectionId={connectionId}
                  zoneId={zoneId}
                  zoneName={zone.data?.name ?? ''}
                  edit={edit}
                />
              </TabsContent>
              <TabsContent value='operations' className='pt-2'>
                <OperationsLog connectionId={connectionId} zoneId={zoneId} />
              </TabsContent>
            </Tabs>
          </>
        )}
      </PageScroll>
      {adding && zone.data && (
        <RecordDialog
          connectionId={connectionId}
          zoneId={zoneId}
          zoneName={zone.data.name}
          onClose={() => setAdding(false)}
        />
      )}
    </Page>
  )
}

function EditStateNotice({ state }: { state: EditState }) {
  if (state.state === 'read-only') {
    return (
      <Alert>
        <ShieldAlert />
        <AlertTitle>Read-only</AlertTitle>
        <AlertDescription>{state.reason}</AlertDescription>
      </Alert>
    )
  }
  if (state.state === 'unknown') {
    return (
      <Alert>
        <Info />
        <AlertTitle>DNS edit permission unknown</AlertTitle>
        <AlertDescription>
          <p>{state.reason}</p>
          <a
            href={SETUP_GUIDE}
            target='_blank'
            rel='noreferrer'
            className='underline underline-offset-2'
          >
            Token permissions
          </a>
        </AlertDescription>
      </Alert>
    )
  }
  return null
}

function RecordsTab({
  connectionId,
  zoneId,
  zoneName,
  edit,
}: {
  connectionId: string
  zoneId: string
  zoneName: string
  edit: EditState
}) {
  const [query, setQuery] = useState('')
  const [type, setType] = useState(ALL_TYPES)
  const [page, setPage] = useState(1)
  const records = useCloudflareDNSRecords(connectionId, zoneId, {
    query,
    type: type === ALL_TYPES ? '' : type,
    page,
    pageSize: PAGE_SIZE,
  })
  const changes = useCloudflareDNSChanges(connectionId, zoneId)
  const [editing, setEditing] = useState<CloudflareDNSRecord>()
  const [deleting, setDeleting] = useState<CloudflareDNSRecord>()

  function toggleProxy(r: CloudflareDNSRecord, proxied: boolean) {
    changes.setProxied.mutate(
      { recordId: r.id, proxied },
      {
        onSuccess: (res) => {
          toastChanged(
            proxied ? `${r.name} is proxied` : `${r.name} is DNS only`,
            res
          )
        },
        onError: (err) => toast.error(changeError(err).message),
      }
    )
  }

  const proxyBusy = (r: CloudflareDNSRecord) =>
    changes.setProxied.isPending &&
    changes.setProxied.variables?.recordId === r.id

  const columns: Column<CloudflareDNSRecord>[] = [
    {
      header: 'Type',
      cell: (r) => <Badge variant='secondary'>{r.type}</Badge>,
    },
    {
      header: 'Name',
      cell: (r) => <span className='font-mono text-xs'>{r.name}</span>,
    },
    {
      header: 'Content',
      cell: (r) => (
        <div className='max-w-md'>
          <div className='truncate font-mono text-xs' title={contentSummary(r)}>
            {contentSummary(r)}
          </div>
          {r.comment && (
            <div
              className='truncate text-xs text-muted-foreground'
              title={r.comment}
            >
              {r.comment}
            </div>
          )}
        </div>
      ),
    },
    {
      header: 'TTL',
      cell: (r) => <span className='text-xs'>{formatTTL(r.ttl)}</span>,
    },
    {
      header: 'Proxy',
      cell: (r) => {
        const a = recordActions(r, edit)
        if (!r.proxiable)
          return <span className='text-xs text-muted-foreground'>—</span>
        return (
          <div className='flex items-center gap-2'>
            <Switch
              checked={r.proxied}
              disabled={!a.proxy || proxyBusy(r)}
              onCheckedChange={(v) => toggleProxy(r, v)}
              aria-label={`Proxy ${r.type} ${r.name}`}
            />
            <span className='text-xs text-muted-foreground'>
              {r.proxied ? 'Proxied' : 'DNS only'}
            </span>
          </div>
        )
      },
    },
    {
      header: 'Actions',
      cell: (r) => {
        const a = recordActions(r, edit)
        if (!a.edit && !a.remove) {
          return (
            <span className='text-xs text-muted-foreground' title={a.reason}>
              {r.editable ? 'Read-only' : 'View only'}
            </span>
          )
        }
        return (
          <div className='flex gap-1'>
            <Button
              size='sm'
              variant='outline'
              onClick={() => setEditing(r)}
              aria-label={`Edit ${r.type} ${r.name}`}
            >
              <Pencil className='h-3.5 w-3.5' />
            </Button>
            <Button
              size='sm'
              variant='outline'
              onClick={() => setDeleting(r)}
              aria-label={`Delete ${r.type} ${r.name}`}
            >
              <Trash2 className='h-3.5 w-3.5' />
            </Button>
          </div>
        )
      },
    },
  ]

  return (
    <>
      <ListFrame
        search={
          <div className='flex w-full flex-col gap-2 sm:flex-row'>
            <SearchBox
              value={query}
              onChange={(v) => {
                setQuery(v)
                setPage(1)
              }}
              placeholder='Search name, content, comment'
            />
            <Select
              value={type}
              onValueChange={(v) => {
                setType(v)
                setPage(1)
              }}
            >
              <SelectTrigger
                className='w-full sm:w-36'
                aria-label='Record type'
              >
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value={ALL_TYPES}>All types</SelectItem>
                {EDITABLE_TYPES.map((t) => (
                  <SelectItem key={t} value={t}>
                    {t}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>
        }
        pager={
          <Pager
            info={records.data?.pageInfo}
            onPage={setPage}
            busy={records.isFetching}
          />
        }
      >
        {records.error ? (
          <ProblemAlert error={records.error} permission='DNS · Read' />
        ) : (
          <DataTable
            columns={columns}
            data={records.data?.records}
            isLoading={records.isLoading}
            emptyMessage={
              query || type !== ALL_TYPES
                ? 'No records match'
                : 'No DNS records'
            }
          />
        )}
      </ListFrame>
      {editing && (
        <RecordDialog
          connectionId={connectionId}
          zoneId={zoneId}
          zoneName={zoneName}
          record={editing}
          onClose={() => setEditing(undefined)}
        />
      )}
      {deleting && (
        <DeleteRecordDialog
          connectionId={connectionId}
          zoneId={zoneId}
          record={deleting}
          onClose={() => setDeleting(undefined)}
        />
      )}
    </>
  )
}

/** Shows the record to be deleted and deletes it once confirmed. */
function DeleteRecordDialog({
  connectionId,
  zoneId,
  record,
  onClose,
}: {
  connectionId: string
  zoneId: string
  record: CloudflareDNSRecord
  onClose: () => void
}) {
  const { remove } = useCloudflareDNSChanges(connectionId, zoneId)
  const [failure, setFailure] = useState<string>()

  function handleDelete() {
    if (remove.isPending) return
    setFailure(undefined)
    remove.mutate(record.id, {
      onSuccess: (res) => {
        toastChanged(`Deleted ${record.type} ${record.name}`, res)
        onClose()
      },
      onError: (err) => setFailure(changeError(err).message),
    })
  }

  return (
    <AlertDialog
      open
      onOpenChange={(open) => !open && !remove.isPending && onClose()}
    >
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>Delete this DNS record?</AlertDialogTitle>
          <AlertDialogDescription>
            It is deleted at Cloudflare right away.
          </AlertDialogDescription>
        </AlertDialogHeader>
        <dl className='grid grid-cols-[5rem_1fr] gap-x-3 gap-y-1 rounded-md border p-3 text-sm'>
          <dt className='text-muted-foreground'>Type</dt>
          <dd>{record.type}</dd>
          <dt className='text-muted-foreground'>Name</dt>
          <dd className='font-mono text-xs break-all'>{record.name}</dd>
          <dt className='text-muted-foreground'>Content</dt>
          <dd className='font-mono text-xs break-all'>
            {contentSummary(record)}
          </dd>
          <dt className='text-muted-foreground'>TTL</dt>
          <dd>{formatTTL(record.ttl)}</dd>
          {record.proxiable && (
            <>
              <dt className='text-muted-foreground'>Proxy</dt>
              <dd>{record.proxied ? 'Proxied' : 'DNS only'}</dd>
            </>
          )}
        </dl>
        {failure && (
          <p className='text-sm break-words text-danger-foreground'>
            {failure}
          </p>
        )}
        <AlertDialogFooter>
          <AlertDialogCancel disabled={remove.isPending}>
            Cancel
          </AlertDialogCancel>
          <Button
            variant='destructive'
            onClick={handleDelete}
            disabled={remove.isPending}
          >
            {remove.isPending ? 'Deleting…' : 'Delete record'}
          </Button>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  )
}
