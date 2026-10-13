import { useState, type FormEvent, type ReactNode } from 'react'
import { CircleAlert, TriangleAlert } from 'lucide-react'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import {
  AlertDialog,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from '@/components/ui/alert-dialog'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { changeError } from './access'
import { nameConfirmed } from './manage'

type Failure = { uncertain: boolean; message: string }

function FailureAlert({ failure, title }: { failure: Failure; title: string }) {
  return (
    <Alert variant={failure.uncertain ? 'default' : 'destructive'}>
      {failure.uncertain ? <TriangleAlert /> : <CircleAlert />}
      <AlertTitle>{failure.uncertain ? 'Result unknown' : title}</AlertTitle>
      <AlertDescription className='break-words'>
        {failure.message}
      </AlertDescription>
    </Alert>
  )
}

export function Field({
  label,
  id,
  error,
  hint,
  children,
}: {
  label: string
  id: string
  error?: string
  hint?: string
  children: ReactNode
}) {
  return (
    <div className='space-y-2'>
      <Label htmlFor={id}>{label}</Label>
      {children}
      {error ? (
        <p className='text-xs text-danger-foreground'>{error}</p>
      ) : hint ? (
        <p className='text-xs text-muted-foreground'>{hint}</p>
      ) : null}
    </div>
  )
}

export type TextFieldSpec = {
  name: string
  label: string
  hint?: string
  placeholder?: string
  initial?: string
  mono?: boolean
  /** Shown as dots; the value is also kept as typed, never trimmed. */
  secret?: boolean
  disabled?: boolean
  /** The problem with the value, or undefined when it is fine. */
  check?: (value: string) => string | undefined
}

export type ToggleSpec = {
  name: string
  label: string
  hint?: string
  initial?: boolean
}

export type FormValues = {
  text: Record<string, string>
  on: Record<string, boolean>
}

/**
 * A dialog that asks for a few values and makes one change with them. `run`
 * resolves once the change was made (and says so itself); a rejection stays
 * in the dialog, with a warning when the change may have been made anyway.
 */
export function FormDialog({
  title,
  description,
  submitLabel,
  pendingLabel = 'Saving…',
  failureTitle = 'Not saved',
  what,
  fields,
  toggles = [],
  children,
  run,
  onClose,
}: {
  title: string
  description?: ReactNode
  submitLabel: string
  pendingLabel?: string
  failureTitle?: string
  /** What to check before trying again, for an unconfirmed change. */
  what: string
  fields: TextFieldSpec[]
  toggles?: ToggleSpec[]
  children?: ReactNode
  run: (values: FormValues) => Promise<void>
  onClose: () => void
}) {
  const [text, setText] = useState(() =>
    Object.fromEntries(fields.map((f) => [f.name, f.initial ?? '']))
  )
  const [on, setOn] = useState(() =>
    Object.fromEntries(toggles.map((t) => [t.name, t.initial ?? false]))
  )
  const [errors, setErrors] = useState<Record<string, string>>({})
  const [failure, setFailure] = useState<Failure>()
  const [pending, setPending] = useState(false)

  async function handleSubmit(e: FormEvent) {
    e.preventDefault()
    if (pending) return
    const clean = Object.fromEntries(
      fields.map((f) => [f.name, f.secret ? text[f.name] : text[f.name].trim()])
    )
    const found: Record<string, string> = {}
    for (const f of fields) {
      const problem = f.check?.(clean[f.name])
      if (problem) found[f.name] = problem
    }
    setErrors(found)
    if (Object.keys(found).length > 0) return
    setFailure(undefined)
    setPending(true)
    try {
      await run({ text: clean, on })
      onClose()
    } catch (err) {
      setFailure(changeError(err, what))
      setPending(false)
    }
  }

  return (
    <Dialog open onOpenChange={(open) => !open && !pending && onClose()}>
      <DialogContent className='sm:max-w-lg'>
        <DialogHeader>
          <DialogTitle>{title}</DialogTitle>
          {description && <DialogDescription>{description}</DialogDescription>}
        </DialogHeader>
        <form onSubmit={handleSubmit} className='space-y-4'>
          {fields.map((f) => (
            <Field
              key={f.name}
              label={f.label}
              id={`field-${f.name}`}
              error={errors[f.name]}
              hint={f.hint}
            >
              <Input
                id={`field-${f.name}`}
                type={f.secret ? 'password' : 'text'}
                value={text[f.name]}
                onChange={(e) =>
                  setText((v) => ({ ...v, [f.name]: e.target.value }))
                }
                placeholder={f.placeholder}
                autoComplete='off'
                disabled={f.disabled}
                className={f.mono ? 'font-mono' : undefined}
              />
            </Field>
          ))}
          {children}
          {toggles.map((t) => (
            <div
              key={t.name}
              className='flex items-start gap-2 rounded-md border p-3'
            >
              <Checkbox
                id={`toggle-${t.name}`}
                checked={on[t.name]}
                onCheckedChange={(v) =>
                  setOn((o) => ({ ...o, [t.name]: v === true }))
                }
              />
              <div>
                <Label htmlFor={`toggle-${t.name}`}>{t.label}</Label>
                {t.hint && (
                  <p className='text-xs text-muted-foreground'>{t.hint}</p>
                )}
              </div>
            </div>
          ))}
          {failure && <FailureAlert failure={failure} title={failureTitle} />}
          <DialogFooter>
            <Button
              type='button'
              variant='outline'
              onClick={onClose}
              disabled={pending}
            >
              Cancel
            </Button>
            <Button type='submit' disabled={pending}>
              {pending ? pendingLabel : submitLabel}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}

/**
 * Asks before a destructive change. With `typeName` the user has to type
 * that name first, for changes that cannot be undone.
 */
export function ConfirmChangeDialog({
  title,
  description,
  confirmLabel,
  pendingLabel = 'Working…',
  what,
  typeName,
  destructive = true,
  children,
  run,
  onClose,
}: {
  title: string
  description: ReactNode
  confirmLabel: string
  pendingLabel?: string
  what: string
  typeName?: string
  destructive?: boolean
  /** Details of what is affected, shown under the description. */
  children?: ReactNode
  run: () => Promise<void>
  onClose: () => void
}) {
  const [typed, setTyped] = useState('')
  const [failure, setFailure] = useState<Failure>()
  const [pending, setPending] = useState(false)
  const ready = !typeName || nameConfirmed(typed, typeName)

  async function handleConfirm() {
    if (pending || !ready) return
    setFailure(undefined)
    setPending(true)
    try {
      await run()
      onClose()
    } catch (err) {
      setFailure(changeError(err, what))
      setPending(false)
    }
  }

  return (
    <AlertDialog open onOpenChange={(open) => !open && !pending && onClose()}>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>{title}</AlertDialogTitle>
          <AlertDialogDescription asChild>
            <div>{description}</div>
          </AlertDialogDescription>
        </AlertDialogHeader>
        {children}
        {typeName && (
          <Field
            label={`Type ${typeName} to confirm`}
            id='confirm-name'
            hint='This cannot be undone.'
          >
            <Input
              id='confirm-name'
              value={typed}
              onChange={(e) => setTyped(e.target.value)}
              autoComplete='off'
              className='font-mono'
            />
          </Field>
        )}
        {failure && <FailureAlert failure={failure} title='Not done' />}
        <AlertDialogFooter>
          <AlertDialogCancel disabled={pending}>Cancel</AlertDialogCancel>
          <Button
            variant={destructive ? 'destructive' : 'default'}
            onClick={handleConfirm}
            disabled={pending || !ready}
          >
            {pending ? pendingLabel : confirmLabel}
          </Button>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  )
}
