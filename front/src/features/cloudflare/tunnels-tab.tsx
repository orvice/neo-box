import { useState, type FormEvent } from 'react'
import { Link } from '@tanstack/react-router'
import { CircleAlert, Plus } from 'lucide-react'
import { toast } from 'sonner'
import {
  useCloudflareTunnelChanges,
  useCloudflareTunnels,
  type CloudflareTunnel,
  type ListQuery,
} from '@/api/cloudflare'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
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
import { DataTable, type Column } from '@/components/data-table'
import { changeError } from './access'
import { ListFrame, Pager, ProblemAlert, SearchBox } from './list-parts'
import { TunnelTokenDialog } from './token-dialog'
import { formatTime, tunnelStatus } from './tunnels'

const PAGE_SIZE = 20

/** The Account's cloudflared Tunnels, with a button to create one. */
export function TunnelsTab({ connectionId }: { connectionId: string }) {
  const [q, setQ] = useState<ListQuery>({
    query: '',
    page: 1,
    pageSize: PAGE_SIZE,
  })
  const tunnels = useCloudflareTunnels(connectionId, q)
  const [creating, setCreating] = useState(false)
  const [created, setCreated] = useState<CloudflareTunnel>()

  const columns: Column<CloudflareTunnel>[] = [
    {
      header: 'Tunnel',
      cell: (t) => (
        <Link
          to='/connections/$connectionId/tunnels/$tunnelId'
          params={{ connectionId, tunnelId: t.id }}
          className='font-medium hover:underline'
        >
          {t.name}
        </Link>
      ),
    },
    { header: 'Status', cell: (t) => <StatusBadge tunnel={t} /> },
    {
      header: 'Connections',
      cell: (t) => <span className='text-xs'>{t.activeConnections}</span>,
    },
    {
      header: 'Configured',
      cell: (t) =>
        t.managedRemotely ? (
          <span className='text-xs'>At Cloudflare</span>
        ) : (
          <Badge variant='outline' title='cloudflared reads a local file'>
            Local file
          </Badge>
        ),
    },
    {
      header: 'Created',
      cell: (t) => (
        <span className='text-xs text-muted-foreground'>
          {formatTime(t.createdAt)}
        </span>
      ),
    },
  ]

  return (
    <>
      <ListFrame
        search={
          <SearchBox
            value={q.query}
            onChange={(query) => setQ((v) => ({ ...v, query, page: 1 }))}
            placeholder='Search Tunnels'
          />
        }
        notice={
          <Button onClick={() => setCreating(true)}>
            <Plus className='h-4 w-4' /> Create Tunnel
          </Button>
        }
        pager={
          <Pager
            info={tunnels.data?.pageInfo}
            onPage={(page) => setQ((v) => ({ ...v, page }))}
            busy={tunnels.isFetching}
          />
        }
      >
        {tunnels.error ? (
          <ProblemAlert
            error={tunnels.error}
            permission='Cloudflare Tunnel · Read'
          />
        ) : (
          <DataTable
            columns={columns}
            data={tunnels.data?.tunnels}
            isLoading={tunnels.isLoading}
            emptyMessage={
              q.query ? 'No Tunnels match' : 'No Tunnels in this account'
            }
            emptyDescription={
              q.query
                ? undefined
                : 'Create one to reach a service without opening a port.'
            }
          />
        )}
      </ListFrame>
      {creating && (
        <CreateTunnelDialog
          connectionId={connectionId}
          onClose={() => setCreating(false)}
          onCreated={(t) => {
            setCreating(false)
            setCreated(t)
          }}
        />
      )}
      {created && (
        <TunnelTokenDialog
          connectionId={connectionId}
          tunnel={created}
          onClose={() => setCreated(undefined)}
        />
      )}
    </>
  )
}

export function StatusBadge({ tunnel }: { tunnel: CloudflareTunnel }) {
  const s = tunnelStatus(tunnel)
  return (
    <Badge className={s.className} title={s.hint}>
      {s.label}
    </Badge>
  )
}

function CreateTunnelDialog({
  connectionId,
  onClose,
  onCreated,
}: {
  connectionId: string
  onClose: () => void
  onCreated: (t: CloudflareTunnel) => void
}) {
  const { create } = useCloudflareTunnelChanges(connectionId)
  const [name, setName] = useState('')
  const [failure, setFailure] = useState<{
    uncertain: boolean
    message: string
  }>()

  function handleSubmit(e: FormEvent) {
    e.preventDefault()
    if (create.isPending) return
    const trimmed = name.trim()
    if (!trimmed) {
      setFailure({ uncertain: false, message: 'Enter a name.' })
      return
    }
    setFailure(undefined)
    create.mutate(trimmed, {
      onSuccess: (tunnel) => {
        if (!tunnel) return
        toast.success(`Created ${tunnel.name}`)
        onCreated(tunnel)
      },
      onError: (err) => setFailure(changeError(err, 'Tunnels')),
    })
  }

  return (
    <Dialog
      open
      onOpenChange={(open) => !open && !create.isPending && onClose()}
    >
      <DialogContent className='sm:max-w-md'>
        <DialogHeader>
          <DialogTitle>Create Tunnel</DialogTitle>
          <DialogDescription>
            A Tunnel whose public hostnames are managed here. Next you get the
            command that connects cloudflared to it.
          </DialogDescription>
        </DialogHeader>
        <form onSubmit={handleSubmit} className='space-y-4'>
          <div className='space-y-2'>
            <Label htmlFor='tunnel-name'>Name</Label>
            <Input
              id='tunnel-name'
              value={name}
              onChange={(e) => setName(e.target.value)}
              placeholder='home-lab'
              autoComplete='off'
              autoFocus
            />
          </div>
          {failure && (
            <Alert variant={failure.uncertain ? 'default' : 'destructive'}>
              <CircleAlert />
              <AlertTitle>
                {failure.uncertain ? 'Result unknown' : 'Not created'}
              </AlertTitle>
              <AlertDescription className='break-words'>
                {failure.message}
              </AlertDescription>
            </Alert>
          )}
          <DialogFooter>
            <Button
              type='button'
              variant='outline'
              onClick={onClose}
              disabled={create.isPending}
            >
              Cancel
            </Button>
            <Button type='submit' disabled={create.isPending}>
              {create.isPending ? 'Creating…' : 'Create'}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
