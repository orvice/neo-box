import type { Connection } from '@/api/connections'
import { Badge } from '@/components/ui/badge'
import { maskApiKey, porkbunConfig } from './format'

/** The connection's Porkbun API key identifier and environment badge. */
export function PorkbunConnectionSummary({
  connection,
}: {
  connection: Connection
}) {
  const cfg = porkbunConfig(connection)
  return (
    <div className='space-y-1 text-sm'>
      <div className='flex items-center gap-2 text-xs text-muted-foreground'>
        <span>API key</span>
        {cfg?.isSandbox && (
          <Badge variant='outline' className='h-5 text-[10px]'>
            Sandbox
          </Badge>
        )}
      </div>
      <div className='font-mono text-xs break-all'>
        {cfg?.apiKey ? maskApiKey(cfg.apiKey) : '-'}
      </div>
    </div>
  )
}
