import { type Connection } from '@/api/connections'
import { cloudflareConfig } from './format'

/** The connection's Account ID. */
export function CloudflareConnectionSummary({
  connection,
}: {
  connection: Connection
}) {
  return (
    <div className='space-y-1 text-sm'>
      <div className='text-xs text-muted-foreground'>Account ID</div>
      <div className='font-mono text-xs break-all'>
        {cloudflareConfig(connection)?.accountId}
      </div>
    </div>
  )
}
