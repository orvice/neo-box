import { toast } from 'sonner'

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
