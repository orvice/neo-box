import { useState, type FormEvent, type ReactNode } from 'react'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Label } from '@/components/ui/label'
import { Textarea } from '@/components/ui/textarea'
import { Field } from './directory-parts'
import {
  MAX_REASON,
  reasonError,
  targetError,
  type GmailConfirmation,
} from './gmail-access'

// Asks why a privileged Drive read is needed and, when `userEmail` is given,
// for that user's primary email typed again. The backend checks both on every
// request; this form only collects them (docs/adr/0009).
export function AccessForm({
  id,
  subject,
  description,
  userEmail,
  expired,
  submitLabel,
  onConfirm,
}: {
  id: string
  // Finishes "Say why you are accessing …".
  subject: string
  description: ReactNode
  userEmail?: string
  expired: boolean
  submitLabel: string
  onConfirm: (c: GmailConfirmation) => void
}) {
  const [reason, setReason] = useState('')
  const [target, setTarget] = useState('')
  const [touched, setTouched] = useState(false)
  const rError = reasonError(reason, subject)
  const tError = userEmail ? targetError(target, userEmail) : ''
  function submit(e: FormEvent) {
    e.preventDefault()
    setTouched(true)
    if (rError || tError) return
    onConfirm({
      reason: reason.trim(),
      targetEmail: userEmail ? target.trim() : '',
      confirmedAt: Date.now(),
    })
  }
  return (
    <form onSubmit={submit} className='max-w-xl space-y-4'>
      <Alert>
        <AlertDescription>
          {description}
          {expired && (
            <p className='mt-2'>
              The previous confirmation expired. Confirm again to continue.
            </p>
          )}
        </AlertDescription>
      </Alert>
      <div className='space-y-2'>
        <Label htmlFor={`${id}-reason`}>Why are you reading this?</Label>
        <Textarea
          id={`${id}-reason`}
          value={reason}
          maxLength={MAX_REASON}
          rows={2}
          onChange={(e) => setReason(e.target.value.replace(/[\r\n]+/g, ' '))}
        />
        <p className='text-xs text-muted-foreground'>
          {[...reason].length}/{MAX_REASON}
        </p>
        {touched && rError && (
          <p role='alert' className='text-sm text-destructive'>
            {rError}
          </p>
        )}
      </div>
      {userEmail && (
        <>
          <Field
            id={`${id}-target`}
            label={`Type ${userEmail} to confirm`}
            value={target}
            onChange={setTarget}
            type='email'
          />
          {touched && tError && (
            <p role='alert' className='text-sm text-destructive'>
              {tError}
            </p>
          )}
        </>
      )}
      <Button>{submitLabel}</Button>
    </form>
  )
}
