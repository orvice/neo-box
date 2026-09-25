import { useState } from 'react'
import { getRouteApi } from '@tanstack/react-router'
import { Cable, Plus } from 'lucide-react'
import { toast } from 'sonner'
import {
  Provider,
  useConnections,
  useDeleteConnection,
  type Connection,
} from '@/api/connections'
import { Button } from '@/components/ui/button'
import { Skeleton } from '@/components/ui/skeleton'
import { Page, PageHeader, PageScroll } from '@/components/common/page-parts'
import { ConfirmDialog } from '@/components/confirm-dialog'
import { EmptyState } from '@/components/empty-state'
import { ConnectionCard } from './connection-card'
import { ConnectionDialog } from './connection-dialog'
import { providerByKey, providerOf } from './providers'

const route = getRouteApi('/_authenticated/connections/')

export function ConnectionsPage() {
  const { provider: providerKey } = route.useSearch()
  const filter = providerByKey(providerKey)
  const { data, isLoading } = useConnections(
    filter?.provider ?? Provider.UNSPECIFIED
  )
  const [adding, setAdding] = useState(false)
  const [editing, setEditing] = useState<Connection | null>(null)
  const [deleting, setDeleting] = useState<Connection | null>(null)
  const remove = useDeleteConnection()

  const addButton = (
    <Button size='sm' onClick={() => setAdding(true)}>
      <Plus />
      Add connection
    </Button>
  )

  return (
    <Page>
      <PageHeader
        title={filter ? `${filter.label} connections` : 'Connections'}
        subtitle={
          filter?.description ??
          'Accounts at third-party services, managed in one place.'
        }
        actions={addButton}
      />
      <PageScroll>
        {isLoading ? (
          <div className='grid gap-4 md:grid-cols-2'>
            <Skeleton className='h-44' />
            <Skeleton className='h-44' />
          </div>
        ) : !data?.length ? (
          <EmptyState
            icon={<Cable className='h-6 w-6' />}
            title={
              filter ? `No ${filter.label} connections` : 'No connections yet'
            }
            description='Connect an account to start managing its resources here.'
            action={addButton}
          />
        ) : (
          <div className='grid gap-4 md:grid-cols-2'>
            {data.map((conn) => (
              <ConnectionCard
                key={conn.id}
                connection={conn}
                onEdit={() => setEditing(conn)}
                onDelete={() => setDeleting(conn)}
              />
            ))}
          </div>
        )}
      </PageScroll>

      {adding && (
        <ConnectionDialog provider={filter} onClose={() => setAdding(false)} />
      )}
      {editing && (
        <ConnectionDialog
          connection={editing}
          onClose={() => setEditing(null)}
        />
      )}

      <ConfirmDialog
        open={!!deleting}
        onOpenChange={(open) => !open && setDeleting(null)}
        title='Delete connection?'
        desc={`"${deleting?.name}" will be removed. ${
          deleting ? (providerOf(deleting.provider)?.deleteWarning ?? '') : ''
        }`}
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
