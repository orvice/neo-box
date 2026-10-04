import { useState, type FormEvent } from 'react'
import { ShieldAlert } from 'lucide-react'
import { toast } from 'sonner'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Switch } from '@/components/ui/switch'
import { PasswordInput } from '@/components/password-input'
import { FormFooter, NameField } from '@/features/connections/form-parts'
import type { ProviderFormProps } from '@/features/connections/types'
import { todayUTC, wasabiConfig } from './format'

const SETUP_GUIDE = 'https://github.com/orvice/neo-box#wasabi'
const DEFAULT_PRICE = 7.99

export function WasabiConnectionForm({
  connection,
  pending,
  onSubmit,
  onCancel,
}: ProviderFormProps) {
  const current = wasabiConfig(connection)
  const [name, setName] = useState(connection?.name ?? '')
  const [accessKeyId, setAccessKeyId] = useState(current?.accessKeyId ?? '')
  const [secretKey, setSecretKey] = useState('')
  const [price, setPrice] = useState(
    String(current?.pricePerTbMonth || DEFAULT_PRICE)
  )
  const [anchor, setAnchor] = useState(current?.billingCycleAnchor ?? '')
  const [estimate, setEstimate] = useState(current?.costEstimateEnabled ?? true)
  const [budget, setBudget] = useState(
    current?.budgetUsd ? String(current.budgetUsd) : ''
  )

  function handleSubmit(e: FormEvent) {
    e.preventDefault()
    if (!name.trim() || !accessKeyId.trim()) {
      toast.error('Name and access key ID are required')
      return
    }
    if (!connection && !secretKey.trim()) {
      toast.error('Secret key is required')
      return
    }
    const perTB = Number.parseFloat(price || '0')
    if (Number.isNaN(perTB) || perTB < 0) {
      toast.error('Price must be 0 or a positive number')
      return
    }
    const budgetUsd = Number.parseFloat(budget || '0')
    if (Number.isNaN(budgetUsd) || budgetUsd < 0) {
      toast.error('Budget must be empty, 0, or a positive number')
      return
    }
    onSubmit(name.trim(), {
      case: 'wasabi',
      value: {
        accessKeyId: accessKeyId.trim(),
        secretKey: secretKey.trim(),
        pricePerTbMonth: perTB,
        billingCycleAnchor: anchor,
        costEstimateEnabled: estimate,
        budgetUsd,
      },
    })
  }

  return (
    <form onSubmit={handleSubmit} className='space-y-4'>
      <Alert>
        <ShieldAlert />
        <AlertTitle>Use a read-only sub-user, not root keys</AlertTitle>
        <AlertDescription>
          The Stats API receives the secret key as-is. Create a sub-user with
          only the Stats policy and use its key.{' '}
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
        placeholder='Wasabi (personal)'
      />
      <div className='space-y-2'>
        <Label htmlFor='conn-access-key'>Access key ID</Label>
        <Input
          id='conn-access-key'
          value={accessKeyId}
          onChange={(e) => setAccessKeyId(e.target.value)}
          autoComplete='off'
          className='font-mono'
        />
      </div>
      <div className='space-y-2'>
        <Label htmlFor='conn-secret-key'>Secret key</Label>
        <PasswordInput
          id='conn-secret-key'
          placeholder={
            connection ? 'Leave empty to keep the current secret key' : ''
          }
          value={secretKey}
          onChange={(e) => setSecretKey(e.target.value)}
          autoComplete='off'
        />
      </div>
      <div className='flex items-center justify-between gap-4 rounded-md border p-3'>
        <div>
          <Label htmlFor='conn-estimate'>Estimate cost</Label>
          <p className='text-xs text-muted-foreground'>
            Turn off for Reserved Capacity plans.
          </p>
        </div>
        <Switch
          id='conn-estimate'
          checked={estimate}
          onCheckedChange={setEstimate}
        />
      </div>
      {estimate && (
        <div className='grid gap-4 sm:grid-cols-2'>
          <div className='space-y-2'>
            <Label htmlFor='conn-price'>Price (USD per TB-month)</Label>
            <Input
              id='conn-price'
              type='number'
              min={0}
              step='0.01'
              value={price}
              onChange={(e) => setPrice(e.target.value)}
            />
          </div>
          <div className='space-y-2'>
            <Label htmlFor='conn-anchor'>Billing cycle start (optional)</Label>
            <Input
              id='conn-anchor'
              type='date'
              max={todayUTC()}
              value={anchor}
              onChange={(e) => setAnchor(e.target.value)}
            />
            <p className='text-xs text-muted-foreground'>
              Any past invoice's start day. Without it, the estimate covers the
              last 30 days.
            </p>
          </div>
          <div className='space-y-2'>
            <Label htmlFor='conn-budget'>
              Budget (USD per cycle, optional)
            </Label>
            <Input
              id='conn-budget'
              type='number'
              min={0}
              step='1'
              placeholder='No budget'
              value={budget}
              onChange={(e) => setBudget(e.target.value)}
            />
            <p className='text-xs text-muted-foreground'>
              Alerts fire when the estimate is on track to go over it, and again
              when it does.
            </p>
          </div>
        </div>
      )}
      <FormFooter pending={pending} onCancel={onCancel} />
    </form>
  )
}
