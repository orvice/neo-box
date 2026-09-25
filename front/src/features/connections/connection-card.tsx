import { Link } from '@tanstack/react-router'
import { Pencil, Plug, Trash2 } from 'lucide-react'
import { toast } from 'sonner'
import {
  ConnectionStatus,
  useTestConnection,
  type Connection,
} from '@/api/connections'
import { formatTime } from '@/lib/format'
import { Button } from '@/components/ui/button'
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from '@/components/ui/card'
import { providerOf } from './providers'
import { ConnectionStatusBadge } from './status-badge'

export function ConnectionCard({
  connection,
  onEdit,
  onDelete,
}: {
  connection: Connection
  onEdit: () => void
  onDelete: () => void
}) {
  const def = providerOf(connection.provider)
  const test = useTestConnection()
  const Icon = def?.icon ?? Plug

  function handleTest() {
    test.mutate(connection.id, {
      onSuccess: (c) =>
        c?.status === ConnectionStatus.OK
          ? toast.success('Connection works')
          : toast.error(c?.statusMessage || 'Connection failed'),
      onError: (e) => toast.error(e.message),
    })
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle className='flex items-center gap-2'>
          <Icon className='h-4 w-4 shrink-0 text-muted-foreground' />
          <Link
            to='/connections/$connectionId'
            params={{ connectionId: connection.id }}
            className='truncate hover:underline'
          >
            {connection.name}
          </Link>
          <ConnectionStatusBadge
            status={connection.status}
            className='ms-auto'
          />
        </CardTitle>
        <CardDescription>
          {def?.label ?? 'Unsupported provider'}
        </CardDescription>
      </CardHeader>
      <CardContent className='space-y-4'>
        {connection.status === ConnectionStatus.ERROR &&
          connection.statusMessage && (
            <p className='text-xs break-words text-danger-foreground'>
              {connection.statusMessage}
            </p>
          )}
        {def && <def.Summary connection={connection} />}
        <div className='text-xs text-muted-foreground'>
          Added {formatTime(connection.createdAt)} · checked{' '}
          {formatTime(connection.statusCheckedAt)}
        </div>
        <div className='flex flex-wrap gap-2'>
          <Button asChild size='sm'>
            <Link
              to='/connections/$connectionId'
              params={{ connectionId: connection.id }}
            >
              Open
            </Link>
          </Button>
          <Button
            size='sm'
            variant='outline'
            onClick={handleTest}
            disabled={test.isPending || !def}
          >
            <Plug />
            {test.isPending ? 'Testing…' : 'Test'}
          </Button>
          <Button size='sm' variant='ghost' onClick={onEdit} disabled={!def}>
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
