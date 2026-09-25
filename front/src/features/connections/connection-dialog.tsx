import { useState } from 'react'
import { toast } from 'sonner'
import {
  useCreateConnection,
  useUpdateConnection,
  type Connection,
  type ConnectionSettings,
} from '@/api/connections'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { providerOf, providers, type ProviderDef } from './providers'

/**
 * Adds a connection (first choosing the provider, unless one is given) or
 * edits one with its provider's form.
 */
export function ConnectionDialog({
  connection,
  provider,
  onClose,
}: {
  connection?: Connection
  provider?: ProviderDef
  onClose: () => void
}) {
  const [picked, setPicked] = useState<ProviderDef | undefined>(
    connection ? providerOf(connection.provider) : provider
  )
  const create = useCreateConnection()
  const update = useUpdateConnection()

  function handleSubmit(name: string, settings: ConnectionSettings) {
    const done = {
      onSuccess: () => {
        toast.success(connection ? 'Connection updated' : 'Connection added')
        onClose()
      },
      onError: (e: Error) => toast.error(e.message),
    }
    if (connection) {
      update.mutate({ id: connection.id, name, settings }, done)
    } else {
      create.mutate({ name, settings }, done)
    }
  }

  const title = connection
    ? 'Edit connection'
    : picked
      ? `Add ${picked.label} connection`
      : 'Add connection'

  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{title}</DialogTitle>
          <DialogDescription>
            {picked ? picked.formHint : 'Choose the service to connect.'}
          </DialogDescription>
        </DialogHeader>
        {picked ? (
          <picked.Form
            connection={connection}
            pending={create.isPending || update.isPending}
            onSubmit={handleSubmit}
            onCancel={onClose}
          />
        ) : (
          <div className='grid gap-2'>
            {providers.map((p) => (
              <button
                key={p.key}
                type='button'
                onClick={() => setPicked(p)}
                className='flex items-start gap-3 rounded-md border p-3 text-start transition-colors hover:bg-accent'
              >
                <p.icon className='mt-0.5 h-5 w-5 shrink-0 text-muted-foreground' />
                <span>
                  <span className='block font-medium'>{p.label}</span>
                  <span className='block text-sm text-muted-foreground'>
                    {p.description}
                  </span>
                </span>
              </button>
            ))}
          </div>
        )}
      </DialogContent>
    </Dialog>
  )
}
