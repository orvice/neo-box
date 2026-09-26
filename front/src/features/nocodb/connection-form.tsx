import { useState, type FormEvent } from 'react'
import { toast } from 'sonner'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { PasswordInput } from '@/components/password-input'
import { FormFooter, NameField } from '@/features/connections/form-parts'
import type { ProviderFormProps } from '@/features/connections/types'
import { nocodbBaseUrl } from './format'

const DEFAULT_RPS = 5

export function NocoDBConnectionForm({
  connection,
  pending,
  onSubmit,
  onCancel,
}: ProviderFormProps) {
  const [name, setName] = useState(connection?.name ?? '')
  const [baseUrl, setBaseUrl] = useState(nocodbBaseUrl(connection))
  const [token, setToken] = useState('')
  const [rps, setRps] = useState(
    String(
      connection?.config.case === 'nocodb'
        ? connection.config.value.requestsPerSecond
        : DEFAULT_RPS
    )
  )

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
    const requestsPerSecond = Number.parseFloat(rps || '0')
    if (
      Number.isNaN(requestsPerSecond) ||
      requestsPerSecond < 0 ||
      requestsPerSecond > 1000
    ) {
      toast.error('Requests per second must be between 0 and 1000')
      return
    }
    onSubmit(name.trim(), {
      case: 'nocodb',
      value: {
        baseUrl: baseUrl.trim(),
        apiToken: token.trim(),
        requestsPerSecond,
      },
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
      <div className='space-y-2'>
        <Label htmlFor='conn-rps'>Requests per second</Label>
        <Input
          id='conn-rps'
          type='number'
          min={0}
          max={1000}
          step='1'
          value={rps}
          onChange={(e) => setRps(e.target.value)}
        />
        <p className='text-xs text-muted-foreground'>
          NocoDB Cloud allows 5. Self-hosted instances can usually go higher,
          which speeds up snapshots of link-heavy Bases.
        </p>
      </div>
      <FormFooter pending={pending} onCancel={onCancel} />
    </form>
  )
}
