import { useState, type FormEvent } from 'react'
import { KeyRound } from 'lucide-react'
import { toast } from 'sonner'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { PasswordInput } from '@/components/password-input'
import { FormFooter, NameField } from '@/features/connections/form-parts'
import type { ProviderFormProps } from '@/features/connections/types'
import {
  ACCOUNT_ID_DOCS,
  CREATE_TOKEN_DOCS,
  SETUP_GUIDE,
  TOKEN_PERMISSIONS,
  cloudflareConfig,
} from './format'

const ACCOUNT_ID = /^[0-9a-f]{32}$/

export function CloudflareConnectionForm({
  connection,
  pending,
  onSubmit,
  onCancel,
}: ProviderFormProps) {
  const current = cloudflareConfig(connection)
  const [name, setName] = useState(connection?.name ?? '')
  const [accountId, setAccountId] = useState(current?.accountId ?? '')
  const [token, setToken] = useState('')
  const normalizedAccount = accountId.trim().toLowerCase()

  function handleSubmit(e: FormEvent) {
    e.preventDefault()
    if (!name.trim()) {
      toast.error('Name is required')
      return
    }
    if (!ACCOUNT_ID.test(normalizedAccount)) {
      toast.error('The Account ID is 32 hexadecimal characters')
      return
    }
    if (!connection && !token.trim()) {
      toast.error('API token is required')
      return
    }
    onSubmit(name.trim(), {
      case: 'cloudflare',
      value: { accountId: normalizedAccount, apiToken: token.trim() },
    })
  }

  return (
    <form onSubmit={handleSubmit} className='space-y-4'>
      <Alert>
        <KeyRound />
        <AlertTitle>Use an API token, read-only where you can</AlertTitle>
        <AlertDescription>
          <p>
            Create a custom token whose resources include this account (
            <a
              href={CREATE_TOKEN_DOCS}
              target='_blank'
              rel='noreferrer'
              className='underline underline-offset-2'
            >
              how
            </a>
            ). Grant only what you need; anything left out shows as missing in
            its tab:
          </p>
          <ul className='mt-1 list-disc ps-4'>
            {TOKEN_PERMISSIONS.map((p) => (
              <li key={p.permission}>
                {p.scope} → {p.permission} — {p.unlocks}
              </li>
            ))}
          </ul>
          <a
            href={SETUP_GUIDE}
            target='_blank'
            rel='noreferrer'
            className='underline underline-offset-2'
          >
            Setup steps
          </a>
        </AlertDescription>
      </Alert>
      <NameField
        value={name}
        onChange={setName}
        placeholder='Cloudflare (personal)'
      />
      <div className='space-y-2'>
        <Label htmlFor='conn-account-id'>Account ID</Label>
        <Input
          id='conn-account-id'
          value={accountId}
          onChange={(e) => setAccountId(e.target.value)}
          autoComplete='off'
          className='font-mono'
          placeholder='32 hexadecimal characters'
          aria-invalid={accountId !== '' && !ACCOUNT_ID.test(normalizedAccount)}
        />
        <p className='text-xs text-muted-foreground'>
          In the dashboard's account home, or in its URL (
          <a
            href={ACCOUNT_ID_DOCS}
            target='_blank'
            rel='noreferrer'
            className='underline underline-offset-2'
          >
            where to find it
          </a>
          ). One connection covers one account.
        </p>
      </div>
      <div className='space-y-2'>
        <Label htmlFor='conn-api-token'>API token</Label>
        <PasswordInput
          id='conn-api-token'
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
