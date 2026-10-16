import { useState } from 'react'
import { Plus, Trash2 } from 'lucide-react'
import {
  useCloudflareEmailChanges,
  type CloudflareEmailAddress,
} from '@/api/cloudflare'
import { cn } from '@/lib/utils'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { DataTable, type Column } from '@/components/data-table'
import { toastChange } from './changed'
import { addressStatus, emailProblem } from './email'
import { ProblemAlert } from './list-parts'
import { ConfirmChangeDialog, FormDialog } from './manage-dialogs'

/**
 * The Account's destination addresses, shared by all its Zones. A new one
 * has to be verified by following the link Cloudflare mails to it.
 */
export function AddressesSection({
  connectionId,
  zoneId,
  addresses,
  error,
  loading,
}: {
  connectionId: string
  zoneId: string
  addresses: CloudflareEmailAddress[] | undefined
  error: unknown
  loading: boolean
}) {
  const changes = useCloudflareEmailChanges(connectionId, zoneId)
  const [adding, setAdding] = useState(false)
  const [deleting, setDeleting] = useState<CloudflareEmailAddress>()

  const columns: Column<CloudflareEmailAddress>[] = [
    {
      header: 'Address',
      cell: (a) => <span className='font-mono text-xs'>{a.email}</span>,
    },
    {
      header: 'Status',
      cell: (a) => {
        const s = addressStatus(a)
        return <Badge className={cn(s.className)}>{s.label}</Badge>
      },
    },
    {
      header: 'Actions',
      cell: (a) => (
        <Button
          size='sm'
          variant='outline'
          onClick={() => setDeleting(a)}
          aria-label={`Delete ${a.email}`}
        >
          <Trash2 className='h-3.5 w-3.5' />
        </Button>
      ),
    },
  ]

  return (
    <section className='space-y-2'>
      <div className='flex flex-wrap items-center justify-between gap-2'>
        <div>
          <h3 className='text-sm font-medium'>Destination addresses</h3>
          <p className='text-xs text-muted-foreground'>
            Shared by every zone of this account. A rule can forward only to a
            verified address.
          </p>
        </div>
        <Button size='sm' onClick={() => setAdding(true)}>
          <Plus className='h-4 w-4' /> Add address
        </Button>
      </div>
      {error ? (
        <ProblemAlert
          error={error}
          permission='Email Routing Addresses · Read'
        />
      ) : (
        <DataTable
          columns={columns}
          data={addresses}
          isLoading={loading}
          emptyMessage='No destination addresses'
        />
      )}
      {adding && (
        <FormDialog
          title='Add a destination address'
          description='Cloudflare emails a verification link to this address. It can be used in a rule once the link is followed.'
          submitLabel='Add address'
          pendingLabel='Adding…'
          what='destination addresses'
          fields={[
            {
              name: 'email',
              label: 'Email address',
              placeholder: 'me@gmail.com',
              mono: true,
              check: emailProblem,
            },
          ]}
          run={async (v) => {
            const res = await changes.createAddress.mutateAsync(v.text.email)
            toastChange(
              `Added ${v.text.email}; a verification email was sent`,
              res.result
            )
          }}
          onClose={() => setAdding(false)}
        />
      )}
      {deleting && (
        <ConfirmChangeDialog
          title='Delete this destination address?'
          description={
            <>
              <span className='font-mono'>{deleting.email}</span> is removed
              from the account. It is refused while a rule or the catch-all of
              this zone still forwards to it; rules of other zones are not
              checked.
            </>
          }
          confirmLabel='Delete address'
          pendingLabel='Deleting…'
          what='destination addresses'
          run={async () => {
            const res = await changes.deleteAddress.mutateAsync(deleting.id)
            toastChange(`Deleted ${deleting.email}`, res.result)
          }}
          onClose={() => setDeleting(undefined)}
        />
      )}
    </section>
  )
}
