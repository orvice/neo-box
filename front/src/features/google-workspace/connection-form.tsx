import { useState, type FormEvent } from 'react'
import { toast } from 'sonner'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Textarea } from '@/components/ui/textarea'
import { FormFooter, NameField } from '@/features/connections/form-parts'
import type { ProviderFormProps } from '@/features/connections/types'

export function WorkspaceConnectionForm({
  connection,
  pending,
  onSubmit,
  onCancel,
}: ProviderFormProps) {
  const current =
    connection?.config.case === 'googleWorkspace'
      ? connection.config.value
      : undefined
  const [name, setName] = useState(connection?.name ?? '')
  const [raw, setRaw] = useState('')
  function submit(e: FormEvent) {
    e.preventDefault()
    if (!name.trim()) return toast.error('Name is required')
    if (!connection && !raw.trim())
      return toast.error('Service Account key JSON is required')
    let serviceAccountEmail = current?.serviceAccountEmail ?? ''
    let clientId = current?.clientId ?? ''
    if (raw.trim()) {
      try {
        const key: Record<string, unknown> = JSON.parse(raw)
        if (
          key.type !== 'service_account' ||
          typeof key.client_email !== 'string' ||
          typeof key.client_id !== 'string' ||
          typeof key.private_key !== 'string'
        )
          throw new Error()
        serviceAccountEmail = key.client_email
        clientId = key.client_id
      } catch {
        return toast.error('Choose a valid Service Account key JSON')
      }
      if (
        current &&
        (current.serviceAccountEmail !== serviceAccountEmail ||
          current.clientId !== clientId)
      )
        return toast.error('Key rotation must use the same Service Account')
    }
    onSubmit(name.trim(), {
      case: 'googleWorkspace',
      value: { serviceAccountEmail, clientId, serviceAccountJson: raw.trim() },
    })
  }
  return (
    <form onSubmit={submit} className='space-y-4'>
      <NameField
        value={name}
        onChange={setName}
        placeholder='Workspace Service Account'
      />
      {current && (
        <p className='text-sm break-all text-muted-foreground'>
          {current.serviceAccountEmail} · Client ID {current.clientId}
        </p>
      )}
      <div className='space-y-2'>
        <Label htmlFor='workspace-key-file'>Service Account key file</Label>
        <Input
          id='workspace-key-file'
          type='file'
          accept='.json,application/json'
          disabled={pending}
          onChange={async (e) => {
            const file = e.target.files?.[0]
            if (!file) return
            if (file.size > 65536) {
              toast.error('Key JSON must be smaller than 64 KB')
              return
            }
            try {
              setRaw(await file.text())
            } catch {
              toast.error('Could not read the key file')
            }
          }}
        />
        <Label htmlFor='workspace-key-json'>Key JSON</Label>
        <Textarea
          id='workspace-key-json'
          value={raw}
          onChange={(e) => setRaw(e.target.value)}
          rows={5}
          autoComplete='off'
          spellCheck={false}
          placeholder={
            connection
              ? 'Leave empty to keep the current key'
              : 'Paste the Service Account key JSON'
          }
        />
        <p className='text-xs text-muted-foreground'>
          The key is encrypted and never returned. Add independent Workspace
          tenants after saving. Rotating the key updates all attached tenants;
          test each tenant afterward.
        </p>
      </div>
      <FormFooter pending={pending} onCancel={onCancel} />
    </form>
  )
}
