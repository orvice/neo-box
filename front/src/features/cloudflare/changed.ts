import { toast } from 'sonner'
import type { CloudflareChangeResult } from '@/api/cloudflare'
import { changeError } from './access'
import { changeOutcome } from './manage'

const PROPAGATION_NOTE =
  'Saved at Cloudflare. Resolvers that cached the old answer keep it until its TTL runs out.'

/**
 * Says a DNS change was made at Cloudflare (not that resolvers see it yet),
 * and warns when its outcome could not be added to the operation log.
 */
export function toastChanged(
  title: string,
  res: { operationLogError: string }
) {
  toast.success(title, { description: PROPAGATION_NOTE })
  if (res.operationLogError) toast.warning(res.operationLogError)
}

/** Tells the user what a successful change did, and what could not be logged. */
export function toastChange(
  title: string,
  result: CloudflareChangeResult | undefined
) {
  const o = changeOutcome(title, {
    changed: result?.changed ?? true,
    operationLogError: result?.operationLogError ?? '',
  })
  if (o.kind === 'success') toast.success(o.title)
  else toast.info(o.title)
  if (o.warning) toast.warning(o.warning, { duration: 10000 })
}

/** Says a change that needed no dialog failed, or may have been made anyway. */
export function toastFailure(err: unknown, what: string) {
  const f = changeError(err, what)
  if (f.uncertain) toast.warning(f.message, { duration: 10000 })
  else toast.error(f.message)
}
