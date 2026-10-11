import { useState, type FormEvent, type ReactNode } from 'react'
import { CircleAlert, TriangleAlert } from 'lucide-react'
import { toast } from 'sonner'
import {
  useCloudflareTunnelHostnames,
  type CloudflareTunnelHostname,
  type CloudflareTunnelRecordResult,
} from '@/api/cloudflare'
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
import {
  emptyHostnameForm,
  formFromHostname,
  recordOutcome,
  validateHostnameForm,
  type HostnameErrors,
  type HostnameForm,
} from './tunnels'

/** Says what happened to the DNS part of a change, when it was asked for. */
function toastRecord(result: CloudflareTunnelRecordResult | undefined) {
  const o = recordOutcome(result)
  if (o.tone === 'success') toast.success(o.message)
  if (o.tone === 'warning') toast.warning(o.message, { duration: 10000 })
}

/** Adds a public hostname to a Tunnel, or edits one (its hostname stays). */
export function HostnameDialog({
  connectionId,
  tunnelId,
  hostname,
  onClose,
}: {
  connectionId: string
  tunnelId: string
  /** The route to edit; undefined adds one. */
  hostname?: CloudflareTunnelHostname
  onClose: () => void
}) {
  const changes = useCloudflareTunnelHostnames(connectionId, tunnelId)
  const [form, setForm] = useState<HostnameForm>(() =>
    hostname ? formFromHostname(hostname) : emptyHostnameForm()
  )
  const [errors, setErrors] = useState<HostnameErrors>({})
  const [failure, setFailure] = useState<{
    uncertain: boolean
    message: string
  }>()
  const pending = changes.add.isPending || changes.update.isPending
  const set = <K extends keyof HostnameForm>(k: K, v: HostnameForm[K]) =>
    setForm((f) => ({ ...f, [k]: v }))

  function handleSubmit(e: FormEvent) {
    e.preventDefault()
    if (pending) return
    const found = validateHostnameForm(form, !hostname)
    setErrors(found)
    if (Object.keys(found).length > 0) return
    setFailure(undefined)
    const path = form.path.trim()
    const service = form.service.trim()
    const onError = (err: unknown) => setFailure(changeError(err, 'hostnames'))
    if (hostname) {
      changes.update.mutate(
        {
          hostname: hostname.hostname,
          currentPath: hostname.path,
          path,
          service,
        },
        {
          onSuccess: () => {
            toast.success('Hostname saved')
            onClose()
          },
          onError,
        }
      )
    } else {
      changes.add.mutate(
        {
          hostname: form.hostname.trim(),
          path,
          service,
          createDns: form.createDns,
        },
        {
          onSuccess: (res) => {
            toast.success('Hostname added')
            toastRecord(res.record)
            onClose()
          },
          onError,
        }
      )
    }
  }

  return (
    <Dialog open onOpenChange={(open) => !open && !pending && onClose()}>
      <DialogContent className='sm:max-w-lg'>
        <DialogHeader>
          <DialogTitle>
            {hostname ? 'Edit public hostname' : 'Add public hostname'}
          </DialogTitle>
          <DialogDescription>
            {hostname
              ? 'Only the path and service change; the route keeps its origin settings.'
              : 'Requests for the hostname reach the service through this Tunnel.'}
          </DialogDescription>
        </DialogHeader>
        <form onSubmit={handleSubmit} className='space-y-4'>
          <Field
            label='Hostname'
            id='tun-hostname'
            error={errors.hostname}
            hint={
              hostname
                ? 'To use another hostname, delete this one and add it again.'
                : 'e.g. app.example.com, or *.example.com'
            }
          >
            <Input
              id='tun-hostname'
              value={form.hostname}
              onChange={(e) => set('hostname', e.target.value)}
              placeholder='app.example.com'
              autoComplete='off'
              disabled={!!hostname}
            />
          </Field>
          <Field
            label='Service'
            id='tun-service'
            error={errors.service}
            hint='Where cloudflared sends the requests, e.g. http://localhost:8080, ssh://host:22, http_status:404'
          >
            <Input
              id='tun-service'
              value={form.service}
              onChange={(e) => set('service', e.target.value)}
              placeholder='http://localhost:8080'
              autoComplete='off'
              className='font-mono'
            />
          </Field>
          <Field
            label='Path (optional)'
            id='tun-path'
            error={errors.path}
            hint='A regular expression on the request path, e.g. ^/api/. Empty matches every path.'
          >
            <Input
              id='tun-path'
              value={form.path}
              onChange={(e) => set('path', e.target.value)}
              autoComplete='off'
              className='font-mono'
            />
          </Field>
          {!hostname && (
            <div className='flex items-start gap-2 rounded-md border p-3'>
              <Checkbox
                id='tun-dns'
                checked={form.createDns}
                onCheckedChange={(v) => set('createDns', v === true)}
              />
              <div>
                <Label htmlFor='tun-dns'>Create the DNS record</Label>
                <p className='text-xs text-muted-foreground'>
                  A proxied CNAME to the Tunnel, when this account has the
                  domain's zone. An existing record is never replaced.
                </p>
              </div>
            </div>
          )}
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

/** Shows the route to be removed and removes it once confirmed. */
export function DeleteHostnameDialog({
  connectionId,
  tunnelId,
  hostname,
  onClose,
}: {
  connectionId: string
  tunnelId: string
  hostname: CloudflareTunnelHostname
  onClose: () => void
}) {
  const { remove } = useCloudflareTunnelHostnames(connectionId, tunnelId)
  const [deleteDns, setDeleteDns] = useState(false)
  const [failure, setFailure] = useState<string>()

  function handleDelete() {
    if (remove.isPending) return
    setFailure(undefined)
    remove.mutate(
      { hostname: hostname.hostname, path: hostname.path, deleteDns },
      {
        onSuccess: (res) => {
          toast.success(`Removed ${hostname.hostname}`)
          toastRecord(res.record)
          onClose()
        },
        onError: (err) => setFailure(changeError(err, 'hostnames').message),
      }
    )
  }

  return (
    <AlertDialog
      open
      onOpenChange={(open) => !open && !remove.isPending && onClose()}
    >
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>Remove this public hostname?</AlertDialogTitle>
          <AlertDialogDescription>
            Requests for it stop reaching the service right away.
          </AlertDialogDescription>
        </AlertDialogHeader>
        <dl className='grid grid-cols-[5rem_1fr] gap-x-3 gap-y-1 rounded-md border p-3 text-sm'>
          <dt className='text-muted-foreground'>Hostname</dt>
          <dd className='font-mono text-xs break-all'>{hostname.hostname}</dd>
          {hostname.path && (
            <>
              <dt className='text-muted-foreground'>Path</dt>
              <dd className='font-mono text-xs break-all'>{hostname.path}</dd>
            </>
          )}
          <dt className='text-muted-foreground'>Service</dt>
          <dd className='font-mono text-xs break-all'>{hostname.service}</dd>
        </dl>
        <div className='flex items-start gap-2 rounded-md border p-3'>
          <Checkbox
            id='tun-del-dns'
            checked={deleteDns}
            onCheckedChange={(v) => setDeleteDns(v === true)}
          />
          <div>
            <Label htmlFor='tun-del-dns'>Also delete its DNS record</Label>
            <p className='text-xs text-muted-foreground'>
              Only a CNAME that points at this Tunnel, and only when no other
              route of the Tunnel uses the hostname.
            </p>
          </div>
        </div>
        {failure && (
          <p className='text-sm break-words text-danger-foreground'>
            {failure}
          </p>
        )}
        <AlertDialogFooter>
          <AlertDialogCancel disabled={remove.isPending}>
            Cancel
          </AlertDialogCancel>
          <Button
            variant='destructive'
            onClick={handleDelete}
            disabled={remove.isPending}
          >
            {remove.isPending ? 'Removing…' : 'Remove hostname'}
          </Button>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
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
