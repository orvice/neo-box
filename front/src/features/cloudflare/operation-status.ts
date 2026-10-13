import { timestampDate, type Timestamp } from '@bufbuild/protobuf/wkt'
import { CloudflareOperationStatus } from '@/api/cloudflare'

export const statusStyle: Record<
  CloudflareOperationStatus,
  { label: string; className: string; hint?: string }
> = {
  [CloudflareOperationStatus.UNSPECIFIED]: {
    label: '-',
    className: 'bg-muted text-muted-foreground',
  },
  [CloudflareOperationStatus.PENDING]: {
    label: 'Pending confirmation',
    className: 'bg-warning-muted text-warning-foreground',
    hint: 'Sent to Cloudflare, but its outcome was never recorded. Check the record.',
  },
  [CloudflareOperationStatus.SUCCEEDED]: {
    label: 'Succeeded',
    className: 'bg-success-muted text-success-foreground',
  },
  [CloudflareOperationStatus.FAILED]: {
    label: 'Failed',
    className: 'bg-danger-muted text-danger-foreground',
  },
  [CloudflareOperationStatus.UNKNOWN]: {
    label: 'Result unknown',
    className: 'bg-warning-muted text-warning-foreground',
    hint: 'Cloudflare never answered; the change may or may not have been made.',
  },
}

/** UTC, as the log is kept. */
export function formatUTC(ts: Timestamp | undefined) {
  if (!ts) return '-'
  return timestampDate(ts).toISOString().replace('T', ' ').slice(0, 19) + ' UTC'
}
