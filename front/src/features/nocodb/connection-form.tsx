import { useState, type FormEvent } from 'react'
import { toast } from 'sonner'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { PasswordInput } from '@/components/password-input'
import { FormFooter, NameField } from '@/features/connections/form-parts'
import type { ProviderFormProps } from '@/features/connections/types'
import { nocodbBaseUrl } from './format'

export function NocoDBConnectionForm({
  connection,
  pending,
  onSubmit,
  onCancel,
}: ProviderFormProps) {
  const [name, setName] = useState(connection?.name ?? '')
  const [baseUrl, setBaseUrl] = useState(nocodbBaseUrl(connection))
  const [token, setToken] = useState('')

  function handleSubmit(e: FormEvent) {
    e.preventDefault()
    if (!name.trim() || !baseUrl.trim()) {
      toast.error('Name and URL are required')
      return
    }
    if (!connection && !token.trim()) {
      toast.error('API token is required')
      return
    }
    onSubmit(name.trim(), {
      case: 'nocodb',
      value: { baseUrl: baseUrl.trim(), apiToken: token.trim() },
    })
  }

  return (
    <form onSubmit={handleSubmit} className='space-y-4'>
      <NameField value={name} onChange={setName} placeholder='Home NocoDB' />
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
          placeholder={
            connection ? 'Leave empty to keep the current token' : ''
          }
          value={token}
          onChange={(e) => setToken(e.target.value)}
          autoComplete='off'
        />
      </div>
      <FormFooter pending={pending} onCancel={onCancel} />
    </form>
  )
}
