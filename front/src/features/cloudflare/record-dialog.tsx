import { useState, type FormEvent, type ReactNode } from 'react'
import { CircleAlert, TriangleAlert } from 'lucide-react'
import {
  useCloudflareDNSChanges,
  type CloudflareDNSRecord,
} from '@/api/cloudflare'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
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
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { Switch } from '@/components/ui/switch'
import { changeError } from './access'
import { toastChanged } from './changed'
import {
  CAA_TAGS,
  EDITABLE_TYPES,
  TTL_OPTIONS,
  emptyForm,
  formFromRecord,
  inputFromForm,
  proxyApplies,
  validateForm,
  type FormErrors,
  type RecordForm,
} from './dns'

/** Adds a record, or edits one (its type stays). */
export function RecordDialog({
  connectionId,
  zoneId,
  zoneName,
  record,
  onClose,
}: {
  connectionId: string
  zoneId: string
  zoneName: string
  /** The record to edit; undefined adds one. */
  record?: CloudflareDNSRecord
  onClose: () => void
}) {
  const changes = useCloudflareDNSChanges(connectionId, zoneId)
  const [form, setForm] = useState<RecordForm>(() =>
    record ? formFromRecord(record) : emptyForm()
  )
  const [errors, setErrors] = useState<FormErrors>({})
  const [failure, setFailure] = useState<{
    uncertain: boolean
    message: string
  }>()
  const pending = changes.create.isPending || changes.update.isPending
  const proxyAllowed = proxyApplies(form.type, record)

  const set = <K extends keyof RecordForm>(k: K, v: RecordForm[K]) =>
    setForm((f) => ({ ...f, [k]: v }))

  function handleSubmit(e: FormEvent) {
    e.preventDefault()
    if (pending) return
    const found = validateForm(form)
    setErrors(found)
    if (Object.keys(found).length > 0) return
    setFailure(undefined)
    const input = inputFromForm(form, proxyAllowed)
    const callbacks = {
      onSuccess: (res: { operationLogError: string }) => {
        toastChanged(record ? 'Record saved' : 'Record added', res)
        onClose()
      },
      onError: (err: unknown) => setFailure(changeError(err)),
    }
    if (record) {
      changes.update.mutate({ recordId: record.id, record: input }, callbacks)
    } else {
      changes.create.mutate(input, callbacks)
    }
  }

  return (
    <Dialog open onOpenChange={(open) => !open && !pending && onClose()}>
      <DialogContent className='sm:max-w-lg'>
        <DialogHeader>
          <DialogTitle>
            {record ? `Edit ${record.type} record` : 'Add DNS record'}
          </DialogTitle>
          <DialogDescription>
            {record
              ? 'Only the fields below change; the record keeps its comment, tags and other settings.'
              : `In ${zoneName}.`}
          </DialogDescription>
        </DialogHeader>
        <form onSubmit={handleSubmit} className='space-y-4'>
          <div className='grid gap-4 sm:grid-cols-[8rem_1fr]'>
            <Field label='Type' id='rec-type' error={errors.type}>
              <Select
                value={form.type}
                onValueChange={(t) =>
                  setForm((f) => ({
                    ...emptyForm(t),
                    name: f.name,
                    ttl: f.ttl,
                  }))
                }
                disabled={!!record}
              >
                <SelectTrigger id='rec-type' className='w-full'>
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {EDITABLE_TYPES.map((t) => (
                    <SelectItem key={t} value={t}>
                      {t}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </Field>
            <Field
              label='Name'
              id='rec-name'
              error={errors.name}
              hint={
                form.type === 'SRV'
                  ? '_service._protocol, e.g. _sip._tcp'
                  : '@ for the zone itself'
              }
            >
              <Input
                id='rec-name'
                value={form.name}
                onChange={(e) => set('name', e.target.value)}
                placeholder={form.type === 'SRV' ? '_sip._tcp' : 'www'}
                autoComplete='off'
              />
            </Field>
          </div>
          <ValueFields form={form} set={set} errors={errors} />
          <div className='grid gap-4 sm:grid-cols-2'>
            <Field label='TTL' id='rec-ttl' error={errors.ttl}>
              <Select value={form.ttl} onValueChange={(v) => set('ttl', v)}>
                <SelectTrigger id='rec-ttl' className='w-full'>
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {!TTL_OPTIONS.some((o) => o.value === form.ttl) && (
                    <SelectItem value={form.ttl}>{form.ttl} s</SelectItem>
                  )}
                  {TTL_OPTIONS.map((o) => (
                    <SelectItem key={o.value} value={o.value}>
                      {o.label}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </Field>
            {proxyAllowed && (
              <div className='flex items-center justify-between gap-4 rounded-md border p-3'>
                <div>
                  <Label htmlFor='rec-proxied'>Proxied</Label>
                  <p className='text-xs text-muted-foreground'>
                    Traffic goes through Cloudflare.
                  </p>
                </div>
                <Switch
                  id='rec-proxied'
                  checked={form.proxied}
                  onCheckedChange={(v) => set('proxied', v)}
                />
              </div>
            )}
          </div>
          {failure && (
            <Alert variant={failure.uncertain ? 'default' : 'destructive'}>
              {failure.uncertain ? <TriangleAlert /> : <CircleAlert />}
              <AlertTitle>
                {failure.uncertain ? 'Result unknown' : 'Not saved'}
              </AlertTitle>
              <AlertDescription className='break-words'>
                {failure.message}
              </AlertDescription>
            </Alert>
          )}
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
              {pending ? 'Saving…' : 'Save'}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}

function Field({
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

const contentLabel: Record<string, { label: string; placeholder: string }> = {
  A: { label: 'IPv4 address', placeholder: '192.0.2.1' },
  AAAA: { label: 'IPv6 address', placeholder: '2001:db8::1' },
  CNAME: { label: 'Target', placeholder: 'example.net' },
  TXT: { label: 'Content', placeholder: 'v=spf1 include:example.net -all' },
  MX: { label: 'Mail server', placeholder: 'mx.example.net' },
  NS: { label: 'Name server', placeholder: 'ns1.example.net' },
}

function ValueFields({
  form,
  set,
  errors,
}: {
  form: RecordForm
  set: <K extends keyof RecordForm>(k: K, v: RecordForm[K]) => void
  errors: FormErrors
}) {
  const num = (k: keyof RecordForm, label: string, max: number) => (
    <Field label={label} id={`rec-${k}`} error={errors[k]}>
      <Input
        id={`rec-${k}`}
        type='number'
        min={0}
        max={max}
        value={form[k] as string}
        onChange={(e) => set(k, e.target.value)}
      />
    </Field>
  )
  if (form.type === 'SRV') {
    return (
      <div className='grid gap-4 sm:grid-cols-3'>
        {num('srvPriority', 'Priority', 65535)}
        {num('srvWeight', 'Weight', 65535)}
        {num('srvPort', 'Port', 65535)}
        <div className='sm:col-span-3'>
          <Field label='Target' id='rec-srv-target' error={errors.srvTarget}>
            <Input
              id='rec-srv-target'
              value={form.srvTarget}
              onChange={(e) => set('srvTarget', e.target.value)}
              placeholder='sip.example.net'
            />
          </Field>
        </div>
      </div>
    )
  }
  if (form.type === 'CAA') {
    return (
      <div className='grid gap-4 sm:grid-cols-[6rem_9rem_1fr]'>
        {num('caaFlags', 'Flags', 255)}
        <Field label='Tag' id='rec-caa-tag' error={errors.caaTag}>
          <Select value={form.caaTag} onValueChange={(v) => set('caaTag', v)}>
            <SelectTrigger id='rec-caa-tag' className='w-full'>
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {CAA_TAGS.map((t) => (
                <SelectItem key={t} value={t}>
                  {t}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </Field>
        <Field label='Value' id='rec-caa-value' error={errors.caaValue}>
          <Input
            id='rec-caa-value'
            value={form.caaValue}
            onChange={(e) => set('caaValue', e.target.value)}
            placeholder='letsencrypt.org'
          />
        </Field>
      </div>
    )
  }
  const c = contentLabel[form.type] ?? contentLabel.TXT
  return (
    <div
      className={form.type === 'MX' ? 'grid gap-4 sm:grid-cols-[1fr_7rem]' : ''}
    >
      <Field label={c.label} id='rec-content' error={errors.content}>
        <Input
          id='rec-content'
          value={form.content}
          onChange={(e) => set('content', e.target.value)}
          placeholder={c.placeholder}
          autoComplete='off'
        />
      </Field>
      {form.type === 'MX' && num('priority', 'Priority', 65535)}
    </div>
  )
}
