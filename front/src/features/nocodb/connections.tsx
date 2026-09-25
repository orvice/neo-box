import { useState } from 'react'
import { Link } from '@tanstack/react-router'
import {
  Database,
  ExternalLink,
  Pencil,
  Plug,
  Plus,
  Trash2,
} from 'lucide-react'
import { toast } from 'sonner'
import {
  useCreateNocoDBConnection,
  useDeleteNocoDBConnection,
  useNocoDBConnections,
  useTestNocoDBConnection,
  useUpdateNocoDBConnection,
  type NocoDBConnection,
} from '@/api/nocodb'
import { Button } from '@/components/ui/button'
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from '@/components/ui/card'
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
import { Skeleton } from '@/components/ui/skeleton'
import { Page, PageHeader, PageScroll } from '@/components/common/page-parts'
import { ConfirmDialog } from '@/components/confirm-dialog'
import { EmptyState } from '@/components/empty-state'
import { PasswordInput } from '@/components/password-input'
import { formatTime } from './format'

export function NocoDBConnectionsPage() {
  const { data, isLoading } = useNocoDBConnections()
  const [editing, setEditing] = useState<NocoDBConnection | 'new' | null>(null)
  const [deleting, setDeleting] = useState<NocoDBConnection | null>(null)
  const remove = useDeleteNocoDBConnection()

  return (
    <Page>
      <PageHeader
        title='NocoDB'
        subtitle='Connect self-hosted NocoDB instances and snapshot their Bases.'
        actions={
          <Button size='sm' onClick={() => setEditing('new')}>
            <Plus />
            Add connection
          </Button>
        }
      />
      <PageScroll>
        {isLoading ? (
          <div className='grid gap-4 md:grid-cols-2'>
            <Skeleton className='h-36' />
            <Skeleton className='h-36' />
          </div>
        ) : !data?.length ? (
          <EmptyState
            icon={<Database className='h-6 w-6' />}
            title='No NocoDB connections'
            description='Add your NocoDB URL and an API token to start backing up Bases.'
            action={
              <Button size='sm' onClick={() => setEditing('new')}>
                <Plus />
                Add connection
              </Button>
            }
          />
        ) : (
          <div className='grid gap-4 md:grid-cols-2'>
            {data.map((conn) => (
              <ConnectionCard
                key={conn.id}
                conn={conn}
                onEdit={() => setEditing(conn)}
                onDelete={() => setDeleting(conn)}
              />
            ))}
          </div>
        )}
      </PageScroll>

      {editing && (
        <ConnectionDialog
          conn={editing === 'new' ? undefined : editing}
          onClose={() => setEditing(null)}
        />
      )}

      <ConfirmDialog
        open={!!deleting}
        onOpenChange={(open) => !open && setDeleting(null)}
        title='Delete connection?'
        desc={`"${deleting?.name}" and all of its snapshots will be permanently deleted. Data in NocoDB is not touched.`}
        destructive
        confirmText='Delete'
        isLoading={remove.isPending}
        handleConfirm={() => {
          if (!deleting) return
          remove.mutate(deleting.id, {
            onSuccess: () => {
              toast.success('Connection deleted')
              setDeleting(null)
            },
            onError: (e) => toast.error(e.message),
          })
        }}
      />
    </Page>
  )
}

function ConnectionCard({
  conn,
  onEdit,
  onDelete,
}: {
  conn: NocoDBConnection
  onEdit: () => void
  onDelete: () => void
}) {
  const test = useTestNocoDBConnection()

  function handleTest() {
    test.mutate(conn.id, {
      onSuccess: (res) =>
        res.ok
          ? toast.success(`Connected — ${res.baseCount} base(s) visible`)
          : toast.error(res.error || 'Connection failed'),
      onError: (e) => toast.error(e.message),
    })
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle className='flex items-center gap-2'>
          <Database className='h-4 w-4 text-muted-foreground' />
          <Link
            to='/nocodb/$connectionId'
            params={{ connectionId: conn.id }}
            className='hover:underline'
          >
            {conn.name}
          </Link>
        </CardTitle>
        <CardDescription className='flex items-center gap-1 break-all'>
          <a
            href={conn.baseUrl}
            target='_blank'
            rel='noreferrer'
            className='inline-flex items-center gap-1 hover:underline'
          >
            {conn.baseUrl}
            <ExternalLink className='h-3 w-3' />
          </a>
        </CardDescription>
      </CardHeader>
      <CardContent className='space-y-4'>
        <div className='text-xs text-muted-foreground'>
          Added {formatTime(conn.createdAt)}
        </div>
        <div className='flex flex-wrap gap-2'>
          <Button asChild size='sm'>
            <Link to='/nocodb/$connectionId' params={{ connectionId: conn.id }}>
              Open bases
            </Link>
          </Button>
          <Button
            size='sm'
            variant='outline'
            onClick={handleTest}
            disabled={test.isPending}
          >
            <Plug />
            {test.isPending ? 'Testing…' : 'Test'}
          </Button>
          <Button size='sm' variant='ghost' onClick={onEdit}>
            <Pencil />
            Edit
          </Button>
          <Button size='sm' variant='ghost' onClick={onDelete}>
            <Trash2 />
            Delete
          </Button>
        </div>
      </CardContent>
    </Card>
  )
}

function ConnectionDialog({
  conn,
  onClose,
}: {
  conn?: NocoDBConnection
  onClose: () => void
}) {
  const create = useCreateNocoDBConnection()
  const update = useUpdateNocoDBConnection()
  const [name, setName] = useState(conn?.name ?? '')
  const [baseUrl, setBaseUrl] = useState(conn?.baseUrl ?? '')
  const [token, setToken] = useState('')
  const pending = create.isPending || update.isPending

  function handleSave() {
    if (!name.trim() || !baseUrl.trim()) {
      toast.error('Name and URL are required')
      return
    }
    const done = {
      onSuccess: () => {
        toast.success(conn ? 'Connection updated' : 'Connection added')
        onClose()
      },
      onError: (e: Error) => toast.error(e.message),
    }
    if (conn) {
      update.mutate(
        {
          id: conn.id,
          name: name.trim(),
          baseUrl: baseUrl.trim(),
          apiToken: token.trim() || undefined,
        },
        done
      )
      return
    }
    if (!token.trim()) {
      toast.error('API token is required')
      return
    }
    create.mutate(
      { name: name.trim(), baseUrl: baseUrl.trim(), apiToken: token.trim() },
      done
    )
  }

  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>
            {conn ? 'Edit connection' : 'Add connection'}
          </DialogTitle>
          <DialogDescription>
            Create an API token in NocoDB under Team &amp; Settings → Tokens. It
            is verified before saving and stored encrypted.
          </DialogDescription>
        </DialogHeader>
        <div className='space-y-4'>
          <div className='space-y-2'>
            <Label htmlFor='conn-name'>Name</Label>
            <Input
              id='conn-name'
              placeholder='Home NocoDB'
              value={name}
              onChange={(e) => setName(e.target.value)}
            />
          </div>
          <div className='space-y-2'>
            <Label htmlFor='conn-url'>NocoDB URL</Label>
            <Input
              id='conn-url'
              placeholder='https://nocodb.example.com'
              value={baseUrl}
              onChange={(e) => setBaseUrl(e.target.value)}
            />
          </div>
          <div className='space-y-2'>
            <Label htmlFor='conn-token'>API token</Label>
            <PasswordInput
              id='conn-token'
              placeholder={conn ? 'Leave empty to keep the current token' : ''}
              value={token}
              onChange={(e) => setToken(e.target.value)}
              autoComplete='off'
            />
          </div>
        </div>
        <DialogFooter>
          <Button variant='outline' onClick={onClose}>
            Cancel
          </Button>
          <Button onClick={handleSave} disabled={pending}>
            {pending ? 'Verifying…' : 'Save'}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
