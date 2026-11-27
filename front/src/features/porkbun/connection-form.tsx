import { useState, type FormEvent } from 'react'
import { KeyRound } from 'lucide-react'
import { toast } from 'sonner'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { PasswordInput } from '@/components/password-input'
import { FormFooter, NameField } from '@/features/connections/form-parts'
import type { ProviderFormProps } from '@/features/connections/types'
import {
  API_ACCESS_DOCS,
  API_DOCS,
  isSandboxKey,
  porkbunConfig,
} from './format'

export function PorkbunConnectionForm({
  connection,
  pending,
  onSubmit,
  onCancel,
}: ProviderFormProps) {
  const current = porkbunConfig(connection)
  const [name, setName] = useState(connection?.name ?? '')
  const [apiKey, setApiKey] = useState(current?.apiKey ?? '')
  const [secretApiKey, setSecretApiKey] = useState('')

  const trimmedKey = apiKey.trim()
  const sandbox = isSandboxKey(trimmedKey)

  function handleSubmit(e: FormEvent) {
    e.preventDefault()
    if (!name.trim()) {
      toast.error('Name is required')
      return
    }
    if (!trimmedKey || /\s/.test(trimmedKey)) {
      toast.error('A valid API key is required')
      return
    }
    if (!connection && !secretApiKey.trim()) {
      toast.error('Secret API key is required')
      return
    }
    onSubmit(name.trim(), {
      case: 'porkbun',
      value: {
        apiKey: trimmedKey,
        secretApiKey: secretApiKey.trim(),
      },
    })
  }

  return (
    <form onSubmit={handleSubmit} className='space-y-4'>
      <Alert>
        <KeyRound />
        <AlertTitle>Read-only API key recommended</AlertTitle>
        <AlertDescription>
          <p>
            Neo Box only reads your Porkbun domains, nameservers, DNS records,
            URL forwards, DNSSEC and glue records — it never modifies anything.
            Create a key pair with{' '}
            <span className='font-medium'>read-only</span> access in{' '}
            <a
              href={API_ACCESS_DOCS}
              target='_blank'
              rel='noreferrer'
              className='underline underline-offset-2'
            >
              Account → API Access
            </a>{' '}
            and opt your domains into API access (
            <a
              href={API_DOCS}
              target='_blank'
              rel='noreferrer'
              className='underline underline-offset-2'
            >
              API docs
            </a>
            ).
          </p>
        </AlertDescription>
      </Alert>

      <NameField
        value={name}
        onChange={setName}
        placeholder='Porkbun (personal)'
      />

      <div className='space-y-2'>
        <div className='flex items-center gap-2'>
          <Label htmlFor='conn-porkbun-api-key'>API key</Label>
          {sandbox && <Badge variant='outline'>Sandbox</Badge>}
        </div>
        <Input
          id='conn-porkbun-api-key'
          value={apiKey}
          onChange={(e) => setApiKey(e.target.value)}
          autoComplete='off'
          className='font-mono'
          placeholder='pk1_...'
        />
        <p className='text-xs text-muted-foreground'>
          Live keys start with <span className='font-mono'>pk1_</span>; sandbox
          keys start with <span className='font-mono'>pk1_sb_</span>.
        </p>
      </div>

      <div className='space-y-2'>
        <Label htmlFor='conn-porkbun-secret-key'>Secret API key</Label>
        <PasswordInput
          id='conn-porkbun-secret-key'
          placeholder={
            connection
              ? 'Leave empty to keep the current secret key'
              : 'sk1_...'
          }
          value={secretApiKey}
          onChange={(e) => setSecretApiKey(e.target.value)}
          autoComplete='off'
        />
      </div>

      <FormFooter pending={pending} onCancel={onCancel} />
    </form>
  )
}
