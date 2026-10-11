import { useState } from 'react'
import { getRouteApi, Link, useNavigate } from '@tanstack/react-router'
import {
  ChevronLeft,
  Copy,
  ExternalLink,
  Info,
  Pencil,
  Plug,
  Plus,
  Trash2,
  TriangleAlert,
} from 'lucide-react'
import { toast } from 'sonner'
import {
  useCloudflareTunnel,
  useCloudflareTunnelChanges,
  type CloudflareTunnel,
  type CloudflareTunnelConnector,
  type CloudflareTunnelHostname,
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
import { Checkbox } from '@/components/ui/checkbox'
import { Label } from '@/components/ui/label'
import { Skeleton } from '@/components/ui/skeleton'
import { Page, PageHeader, PageScroll } from '@/components/common/page-parts'
import { DataTable, type Column } from '@/components/data-table'
import { changeError } from './access'
import { copy } from './clipboard'
import { DeleteHostnameDialog, HostnameDialog } from './hostname-dialog'
import { ProblemAlert, RefreshButton } from './list-parts'
import { TunnelTokenDialog } from './token-dialog'
import { formatTime, hostnameUrl } from './tunnels'
import { StatusBadge } from './tunnels-tab'

const route = getRouteApi(
  '/_authenticated/connections/$connectionId/tunnels/$tunnelId'
)

/** One Tunnel: its connectors, and the public hostnames it serves. */
export function CloudflareTunnelPage() {
  const { connectionId, tunnelId } = route.useParams()
  const connection = useConnection(connectionId)
  const detail = useCloudflareTunnel(connectionId, tunnelId)
  const tunnel = detail.data?.tunnel
  const [showToken, setShowToken] = useState(false)
  const [deleting, setDeleting] = useState(false)

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
          tunnel?.name ??
          (detail.isLoading ? <Skeleton className='h-8 w-48' /> : 'Tunnel')
        }
        subtitle={
          tunnel && (
            <span className='inline-flex flex-wrap items-center gap-2'>
              <StatusBadge tunnel={tunnel} />
              <span className='font-mono text-xs'>{tunnel.id}</span>
              <span className='text-xs'>
                Created {formatTime(tunnel.createdAt)}
              </span>
            </span>
          )
        }
        actions={
          <>
            <RefreshButton connectionId={connectionId} />
            <Button
              variant='outline'
              onClick={() => setShowToken(true)}
              disabled={!tunnel}
            >
              <Plug className='h-4 w-4' /> Connect cloudflared
            </Button>
            <Button
              variant='outline'
              onClick={() => setDeleting(true)}
              disabled={!tunnel}
            >
              <Trash2 className='h-4 w-4' /> Delete
            </Button>
          </>
        }
      />
      <PageScroll className='space-y-6'>
        {detail.error ? (
          <ProblemAlert
            error={detail.error}
            permission='Cloudflare Tunnel · Read'
          />
        ) : (
          <>
            {detail.data?.issues.length ? (
              <Alert>
                <TriangleAlert />
                <AlertTitle>Some of this could not be read</AlertTitle>
                <AlertDescription>
                  <ul className='list-disc ps-4'>
                    {detail.data.issues.map((i) => (
                      <li key={i.source}>
                        {i.source}: {i.message}
                        {i.permissionDenied &&
                          ' Add "Cloudflare Tunnel · Read" to the token.'}
                      </li>
                    ))}
                  </ul>
                </AlertDescription>
              </Alert>
            ) : null}
            <HostnamesSection
              connectionId={connectionId}
              tunnelId={tunnelId}
              tunnel={tunnel}
              hostnames={detail.data?.hostnames}
              catchAll={detail.data?.catchAllService ?? ''}
              loading={detail.isLoading}
            />
            <ConnectorsSection
              connectors={detail.data?.connectors}
              loading={detail.isLoading}
            />
          </>
        )}
      </PageScroll>
      {showToken && tunnel && (
        <TunnelTokenDialog
          connectionId={connectionId}
          tunnel={tunnel}
          onClose={() => setShowToken(false)}
        />
      )}
      {deleting && tunnel && (
        <DeleteTunnelDialog
          connectionId={connectionId}
          tunnel={tunnel}
          onClose={() => setDeleting(false)}
        />
      )}
    </Page>
  )
}

function HostnamesSection({
  connectionId,
  tunnelId,
  tunnel,
  hostnames,
  catchAll,
  loading,
}: {
  connectionId: string
  tunnelId: string
  tunnel: CloudflareTunnel | undefined
  hostnames: CloudflareTunnelHostname[] | undefined
  catchAll: string
  loading: boolean
}) {
  const [adding, setAdding] = useState(false)
  const [editing, setEditing] = useState<CloudflareTunnelHostname>()
  const [deleting, setDeleting] = useState<CloudflareTunnelHostname>()
  const remote = tunnel?.managedRemotely ?? false

  const columns: Column<CloudflareTunnelHostname>[] = [
    {
      header: 'Hostname',
      cell: (h) => {
        const url = hostnameUrl(h)
        return (
          <div className='flex min-w-0 items-center gap-1.5'>
            {url ? (
              <a
                href={url}
                target='_blank'
                rel='noreferrer noopener'
                className='truncate font-mono text-xs hover:underline'
              >
                {h.hostname}
              </a>
            ) : (
              <span
                className='truncate font-mono text-xs'
                title={h.hostname ? 'A pattern, not a URL' : undefined}
              >
                {h.hostname || '(any hostname)'}
              </span>
            )}
            {url && (
              <>
                <Button asChild size='icon' variant='ghost' className='size-6'>
                  <a
                    href={url}
                    target='_blank'
                    rel='noreferrer noopener'
                    aria-label={`Open ${h.hostname}`}
                  >
                    <ExternalLink className='h-3.5 w-3.5' />
                  </a>
                </Button>
                <Button
                  size='icon'
                  variant='ghost'
                  className='size-6'
                  onClick={() => void copy(url)}
                  aria-label={`Copy ${h.hostname}`}
                >
                  <Copy className='h-3.5 w-3.5' />
                </Button>
              </>
            )}
          </div>
        )
      },
    },
    {
      header: 'Path',
      cell: (h) =>
        h.path ? (
          <span className='font-mono text-xs'>{h.path}</span>
        ) : (
          <span className='text-xs text-muted-foreground'>All</span>
        ),
    },
    {
      header: 'Service',
      cell: (h) => (
        <div className='flex items-center gap-1.5'>
          <span className='font-mono text-xs break-all'>{h.service}</span>
          {h.hasOriginSettings && (
            <Badge
              variant='outline'
              title='Origin settings (originRequest) are kept when the route changes'
            >
              Origin settings
            </Badge>
          )}
        </div>
      ),
    },
    {
      header: 'Actions',
      cell: (h) => (
        <div className='flex gap-1'>
          <Button
            size='sm'
            variant='outline'
            onClick={() => setEditing(h)}
            aria-label={`Edit ${h.hostname}`}
          >
            <Pencil className='h-3.5 w-3.5' />
          </Button>
          <Button
            size='sm'
            variant='outline'
            onClick={() => setDeleting(h)}
            aria-label={`Remove ${h.hostname}`}
          >
            <Trash2 className='h-3.5 w-3.5' />
          </Button>
        </div>
      ),
    },
  ]

  return (
    <section className='space-y-3'>
      <div className='flex items-center justify-between gap-2'>
        <div>
          <h2 className='text-lg font-semibold'>Public hostnames</h2>
          <p className='text-sm text-muted-foreground'>
            Routes in the order cloudflared applies them.
            {catchAll && (
              <>
                {' '}
                Everything else goes to{' '}
                <span className='font-mono text-xs'>{catchAll}</span>.
              </>
            )}
          </p>
        </div>
        <Button
          onClick={() => setAdding(true)}
          disabled={!remote}
          title={remote ? undefined : 'This Tunnel is configured by a file'}
        >
          <Plus className='h-4 w-4' /> Add hostname
        </Button>
      </div>
      {tunnel && !remote && (
        <Alert>
          <Info />
          <AlertTitle>Configured by a local file</AlertTitle>
          <AlertDescription>
            cloudflared reads this Tunnel's hostnames from its own config file,
            so Cloudflare holds none to show or change. Create a Tunnel here to
            manage hostnames from Neo Box.
          </AlertDescription>
        </Alert>
      )}
      {(remote || loading) && (
        <DataTable
          columns={columns}
          data={hostnames}
          isLoading={loading}
          emptyMessage='No public hostnames'
          emptyDescription='Add one to send a domain through this Tunnel.'
        />
      )}
      {adding && (
        <HostnameDialog
          connectionId={connectionId}
          tunnelId={tunnelId}
          onClose={() => setAdding(false)}
        />
      )}
      {editing && (
        <HostnameDialog
          connectionId={connectionId}
          tunnelId={tunnelId}
          hostname={editing}
          onClose={() => setEditing(undefined)}
        />
      )}
      {deleting && (
        <DeleteHostnameDialog
          connectionId={connectionId}
          tunnelId={tunnelId}
          hostname={deleting}
          onClose={() => setDeleting(undefined)}
        />
      )}
    </section>
  )
}

function ConnectorsSection({
  connectors,
  loading,
}: {
  connectors: CloudflareTunnelConnector[] | undefined
  loading: boolean
}) {
  const columns: Column<CloudflareTunnelConnector>[] = [
    {
      header: 'Connector',
      cell: (c) => (
        <span className='font-mono text-xs' title={c.id}>
          {c.id.slice(0, 8)}
        </span>
      ),
    },
    {
      header: 'Version',
      cell: (c) => (
        <span className='text-xs'>
          {c.version || '-'}
          {c.arch && <span className='text-muted-foreground'> · {c.arch}</span>}
        </span>
      ),
    },
    {
      header: 'Data centers',
      cell: (c) => (
        <div className='flex flex-wrap gap-1'>
          {c.connections.map((conn) => (
            <Badge
              key={conn.id}
              variant='secondary'
              title={`${conn.originIp} · opened ${formatTime(conn.openedAt)}${
                conn.pendingReconnect ? ' · reconnecting' : ''
              }`}
            >
              {conn.coloName || '?'}
              {conn.pendingReconnect && ' …'}
            </Badge>
          ))}
        </div>
      ),
    },
    {
      header: 'Running since',
      cell: (c) => (
        <span className='text-xs text-muted-foreground'>
          {formatTime(c.runAt)}
        </span>
      ),
    },
  ]
  return (
    <section className='space-y-3'>
      <div>
        <h2 className='text-lg font-semibold'>Connectors</h2>
        <p className='text-sm text-muted-foreground'>
          The cloudflared instances connected to this Tunnel now.
        </p>
      </div>
      <DataTable
        columns={columns}
        data={connectors}
        isLoading={loading}
        emptyMessage='No connector is connected'
        emptyDescription='Run cloudflared with the Tunnel token to bring it up.'
      />
    </section>
  )
}

/** Confirms, then deletes the Tunnel and goes back to the connection. */
function DeleteTunnelDialog({
  connectionId,
  tunnel,
  onClose,
}: {
  connectionId: string
  tunnel: CloudflareTunnel
  onClose: () => void
}) {
  const { remove } = useCloudflareTunnelChanges(connectionId)
  const navigate = useNavigate()
  const [cleanup, setCleanup] = useState(false)
  const [failure, setFailure] = useState<string>()

  function handleDelete() {
    if (remove.isPending) return
    setFailure(undefined)
    remove.mutate(
      { tunnelId: tunnel.id, cleanupConnections: cleanup },
      {
        onSuccess: () => {
          toast.success(`Deleted ${tunnel.name}`)
          void navigate({
            to: '/connections/$connectionId',
            params: { connectionId },
          })
        },
        onError: (err) => setFailure(changeError(err, 'Tunnels').message),
      }
    )
  }

  return (
    <AlertDialog
      open
      onOpenChange={(open) => !open && !remove.isPending && onClose()}
    >
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>Delete {tunnel.name}?</AlertDialogTitle>
          <AlertDialogDescription>
            Its public hostnames stop working. DNS records that point at it are
            not deleted.
          </AlertDialogDescription>
        </AlertDialogHeader>
        {tunnel.activeConnections > 0 && (
          <Alert>
            <TriangleAlert />
            <AlertTitle>cloudflared is still connected</AlertTitle>
            <AlertDescription>
              Cloudflare refuses to delete a Tunnel in use. Stop cloudflared
              first, or it reconnects.
            </AlertDescription>
          </Alert>
        )}
        <div className='flex items-start gap-2 rounded-md border p-3'>
          <Checkbox
            id='tun-cleanup'
            checked={cleanup}
            onCheckedChange={(v) => setCleanup(v === true)}
          />
          <div>
            <Label htmlFor='tun-cleanup'>Clean up its connections first</Label>
            <p className='text-xs text-muted-foreground'>
              Closes stale connections that Cloudflare still lists, which
              otherwise block the delete.
            </p>
          </div>
        </div>
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
            {remove.isPending ? 'Deleting…' : 'Delete Tunnel'}
          </Button>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  )
}
