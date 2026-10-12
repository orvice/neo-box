import { useState } from 'react'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { ConfirmDialog } from '@/components/confirm-dialog'
import type { useCursor } from './directory-state'

export function DirectoryError({
  error,
  scope,
}: {
  error: unknown
  scope?: string
}) {
  if (!error) return null
  return (
    <Alert variant='destructive'>
      <AlertDescription>
        <p>{String(error)}</p>
        {scope && (
          <p className='mt-2 break-all'>
            Check this tenant's delegation and administrator privileges for{' '}
            <code>{scope}</code>.
          </p>
        )}
      </AlertDescription>
    </Alert>
  )
}
export function Field({
  id,
  label,
  value,
  onChange,
  type = 'text',
  required = false,
}: {
  id: string
  label: string
  value: string
  onChange: (value: string) => void
  type?: string
  required?: boolean
}) {
  return (
    <div className='space-y-2'>
      <Label htmlFor={id}>{label}</Label>
      <Input
        id={id}
        value={value}
        onChange={(e) => onChange(e.target.value)}
        type={type}
        required={required}
      />
    </div>
  )
}
export function CursorPager({
  cursor,
  nextToken,
  busy,
}: {
  cursor: ReturnType<typeof useCursor>
  nextToken?: string
  busy: boolean
}) {
  return (
    <div className='flex items-center justify-end gap-3'>
      <span className='text-sm text-muted-foreground'>Page {cursor.page}</span>
      <Button
        variant='outline'
        disabled={cursor.page === 1 || busy}
        onClick={cursor.previous}
      >
        Previous
      </Button>
      <Button
        variant='outline'
        disabled={!nextToken || busy}
        onClick={() => {
          if (nextToken) cursor.next(nextToken)
        }}
      >
        Next
      </Button>
    </div>
  )
}
// The dialog remains open on failed or unconfirmed writes and shows the outcome.
export function DirectoryConfirm({
  title,
  description,
  confirmText,
  run,
  onClose,
}: {
  title: string
  description: string
  confirmText: string
  run: () => Promise<unknown>
  onClose: () => void
}) {
  const [pending, setPending] = useState(false)
  const [error, setError] = useState('')
  return (
    <ConfirmDialog
      open
      onOpenChange={(open) => {
        if (!open && !pending) onClose()
      }}
      title={title}
      desc={
        <div>
          <p>{description}</p>
          {error && (
            <p role='alert' className='mt-2 text-destructive'>
              {error}
            </p>
          )}
        </div>
      }
      confirmText={confirmText}
      destructive
      isLoading={pending}
      handleConfirm={async () => {
        setError('')
        setPending(true)
        try {
          await run()
          onClose()
        } catch (e) {
          setError(String(e))
        } finally {
          setPending(false)
        }
      }}
    />
  )
}
export function FormButtons({
  pending,
  onCancel,
}: {
  pending: boolean
  onCancel: () => void
}) {
  return (
    <div className='flex justify-end gap-2'>
      <Button
        type='button'
        variant='outline'
        disabled={pending}
        onClick={onCancel}
      >
        Cancel
      </Button>
      <Button disabled={pending}>{pending ? 'Saving…' : 'Save'}</Button>
    </div>
  )
}
